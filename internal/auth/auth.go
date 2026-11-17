// Package auth turns an auth profile plus the application's cert assets into
// usable credentials: headers, query parameters, a username/password pair
// and a TLS configuration.
package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"

	"github.com/testplane/testplane/internal/model"
)

// Credentials is what a step executor needs from a profile.
type Credentials struct {
	ProfileID string
	Headers   map[string]string
	Query     map[string]string
	Username  string
	Password  string
	TLS       *tls.Config
	// Absolute paths, for clients that take files instead of a tls.Config.
	CertFile, KeyFile, CAFile string
	ServerName                string
	InsecureSkipVerify        bool
}

// None is the empty credential set.
func None() *Credentials {
	return &Credentials{Headers: map[string]string{}, Query: map[string]string{}}
}

// Resolver resolves profiles and caches the result (including fetched
// tokens) for the lifetime of one run.
type Resolver struct {
	CertsDir string
	mu       sync.Mutex
	cache    map[string]*Credentials
}

func NewResolver(certsDir string) *Resolver {
	return &Resolver{CertsDir: certsDir, cache: map[string]*Credentials{}}
}

// Resolve returns credentials for an already-templated profile.
func (r *Resolver) Resolve(ctx context.Context, p *model.AuthProfile) (*Credentials, error) {
	if p == nil || p.Type == model.AuthNone || p.Type == "" {
		return None(), nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cache[p.ID]; ok {
		return c, nil
	}
	c, err := r.resolve(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("auth profile %q: %w", p.ID, err)
	}
	r.cache[p.ID] = c
	return c, nil
}

func (r *Resolver) resolve(ctx context.Context, p *model.AuthProfile) (*Credentials, error) {
	if errs := p.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	c := None()
	c.ProfileID = p.ID
	if p.TLS != nil {
		if err := r.loadTLS(c, p.TLS); err != nil {
			return nil, err
		}
	}
	switch p.Type {
	case model.AuthBasic:
		c.Username, c.Password = p.Basic.Username, p.Basic.Password
		c.Headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password))
	case model.AuthBearer:
		c.Headers["Authorization"] = joinPrefix(p.Bearer.Prefix, p.Bearer.Token)
	case model.AuthAPIKey:
		if p.APIKey.In == "query" {
			c.Query[p.APIKey.Name] = p.APIKey.Value
		} else {
			c.Headers[p.APIKey.Name] = p.APIKey.Value
		}
	case model.AuthOAuth2ClientCredentials, model.AuthOAuth2Password:
		tok, err := fetchToken(ctx, p, c.TLS)
		if err != nil {
			return nil, err
		}
		name := p.OAuth2.HeaderName
		if name == "" {
			name = "Authorization"
		}
		c.Headers[name] = joinPrefix(p.OAuth2.Prefix, tok)
	}
	return c, nil
}

func joinPrefix(prefix, token string) string {
	if prefix == "" {
		prefix = "Bearer"
	}
	if prefix == "none" {
		return token
	}
	return prefix + " " + token
}

func (r *Resolver) loadTLS(c *Credentials, t *model.TLSSettings) error {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.ServerName, InsecureSkipVerify: t.InsecureSkipVerify}
	c.ServerName, c.InsecureSkipVerify = t.ServerName, t.InsecureSkipVerify
	if t.Cert != "" {
		c.CertFile = filepath.Join(r.CertsDir, t.Cert)
		c.KeyFile = filepath.Join(r.CertsDir, t.Key)
		pair, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return fmt.Errorf("client certificate %s / key %s: %w", t.Cert, t.Key, err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	if t.CA != "" {
		c.CAFile = filepath.Join(r.CertsDir, t.CA)
		pemBytes, err := os.ReadFile(c.CAFile)
		if err != nil {
			return fmt.Errorf("CA bundle %s: %w", t.CA, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return fmt.Errorf("CA bundle %s holds no PEM certificates", t.CA)
		}
		cfg.RootCAs = pool
	}
	c.TLS = cfg
	return nil
}

func fetchToken(ctx context.Context, p *model.AuthProfile, tlsCfg *tls.Config) (string, error) {
	o := p.OAuth2
	form := url.Values{}
	if p.Type == model.AuthOAuth2Password {
		form.Set("grant_type", "password")
		form.Set("username", o.Username)
		form.Set("password", o.Password)
	} else {
		form.Set("grant_type", "client_credentials")
	}
	if len(o.Scopes) > 0 {
		form.Set("scope", strings.Join(o.Scopes, " "))
	}
	if o.Audience != "" {
		form.Set("audience", o.Audience)
	}
	for k, v := range o.ExtraParams {
		form.Set(k, v)
	}
	if o.ClientAuth != "basic" {
		form.Set("client_id", o.ClientID)
		if o.ClientSecret != "" {
			form.Set("client_secret", o.ClientSecret)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if o.ClientAuth == "basic" {
		req.SetBasicAuth(o.ClientID, o.ClientSecret)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if tlsCfg != nil {
		tr.TLSClientConfig = tlsCfg
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return "", fmt.Errorf("token request to %s: %w", o.TokenURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("token endpoint %s answered %d: %s", o.TokenURL, resp.StatusCode, truncate(string(body), 300))
	}
	field := o.TokenField
	if field == "" {
		field = "access_token"
	}
	if !json.Valid(body) {
		return "", fmt.Errorf("token endpoint %s did not return JSON", o.TokenURL)
	}
	tok := gjson.GetBytes(body, field).String()
	if tok == "" {
		return "", fmt.Errorf("token endpoint %s response has no %q field", o.TokenURL, field)
	}
	return tok, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Check is one line of the "what does this profile need" list shown in the
// UI and printed by the CLI.
type Check struct {
	Field    string `json:"field"`
	Label    string `json:"label"`
	Help     string `json:"help"`
	Required bool   `json:"required"`
	File     string `json:"file,omitempty"`
	Status   string `json:"status"` // ok | missing | not_set | wrong_kind | expired | mismatch
	Detail   string `json:"detail,omitempty"`
}

// CertChecks lists the certificate files a profile needs and whether each
// is in place. It returns nil when the profile uses no TLS material.
func CertChecks(p *model.AuthProfile, certsDir string) []Check {
	if p.TLS == nil && p.Type != model.AuthMTLS {
		return nil
	}
	t := p.TLS
	if t == nil {
		t = &model.TLSSettings{}
	}
	mtls := p.Type == model.AuthMTLS || t.Cert != "" || t.Key != ""
	checks := []Check{
		fileCheck(certsDir, "tls.cert", "Client certificate", "PEM certificate (plus any intermediates) issued to this test client by a CA the service trusts.", mtls, t.Cert, "CERTIFICATE"),
		fileCheck(certsDir, "tls.key", "Client private key", "Unencrypted PEM private key that matches the client certificate.", mtls, t.Key, "PRIVATE KEY"),
		fileCheck(certsDir, "tls.ca", "CA bundle", "PEM certificate(s) of the CA that signed the service's own certificate. Only needed when that CA is private; public CAs are trusted already.", false, t.CA, "CERTIFICATE"),
	}
	if checks[0].Status == "ok" && checks[1].Status == "ok" {
		if _, err := tls.LoadX509KeyPair(filepath.Join(certsDir, t.Cert), filepath.Join(certsDir, t.Key)); err != nil {
			checks[1].Status = "mismatch"
			checks[1].Detail = "key does not match the certificate: " + err.Error()
		}
	}
	return checks
}

func fileCheck(dir, field, label, help string, required bool, file, pemType string) Check {
	c := Check{Field: field, Label: label, Help: help, Required: required, File: file}
	if file == "" {
		c.Status = "not_set"
		return c
	}
	if !model.ValidFileName(file) {
		c.Status, c.Detail = "missing", "not a plain file name"
		return c
	}
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		c.Status, c.Detail = "missing", "upload "+file+" to this application's certs"
		return c
	}
	found := false
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			break
		}
		if !strings.Contains(blk.Type, pemType) {
			continue
		}
		found = true
		if blk.Type == "CERTIFICATE" {
			if cert, err := x509.ParseCertificate(blk.Bytes); err == nil {
				c.Detail = fmt.Sprintf("%s, valid until %s", cert.Subject.String(), cert.NotAfter.UTC().Format("2006-01-02"))
				if time.Now().After(cert.NotAfter) {
					c.Status = "expired"
					return c
				}
			}
		}
		break
	}
	if !found {
		c.Status, c.Detail = "wrong_kind", "file holds no PEM "+pemType+" block"
		return c
	}
	c.Status = "ok"
	return c
}
