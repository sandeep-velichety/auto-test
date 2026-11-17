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

// Resolve returns credentials for an already-templated profile, with
// certificate files taken from the resolver's own directory.
func (r *Resolver) Resolve(ctx context.Context, p *model.AuthProfile) (*Credentials, error) {
	if p == nil {
		return None(), nil
	}
	return r.ResolveIn(ctx, p, r.CertsDir, p.ID)
}

// ResolveIn is Resolve with an explicit certificate directory and cache
// key, so one resolver can serve several applications and the same profile
// combined with different certificate sets.
func (r *Resolver) ResolveIn(ctx context.Context, p *model.AuthProfile, certsDir, key string) (*Credentials, error) {
	if p == nil || ((p.Type == model.AuthNone || p.Type == "") && p.TLS == nil && len(p.Headers) == 0) {
		return None(), nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cache[key]; ok {
		return c, nil
	}
	c, err := r.resolve(ctx, p, certsDir)
	if err != nil {
		return nil, fmt.Errorf("auth profile %q: %w", p.ID, err)
	}
	r.cache[key] = c
	return c, nil
}

func (r *Resolver) resolve(ctx context.Context, p *model.AuthProfile, certsDir string) (*Credentials, error) {
	if p.Type == "" {
		p.Type = model.AuthNone
	}
	if errs := p.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	c := None()
	c.ProfileID = p.ID
	if p.TLS != nil {
		if err := loadTLS(c, p.TLS, certsDir); err != nil {
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
	case model.AuthHTTPCall:
		tok, err := callForToken(ctx, p.HTTPCall, c.TLS)
		if err != nil {
			return nil, err
		}
		name := p.HTTPCall.HeaderName
		if name == "" {
			name = "Authorization"
		}
		c.Headers[name] = joinPrefix(p.HTTPCall.Prefix, tok)
	}
	// Fixed headers go last so they can add to, or replace, what the type set.
	for k, v := range p.Headers {
		if v != "" {
			c.Headers[k] = v
		}
	}
	return c, nil
}

// callForToken runs the custom token request of an http_call profile.
func callForToken(ctx context.Context, h *model.HTTPCallAuth, tlsCfg *tls.Config) (string, error) {
	u, err := url.Parse(h.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("auth call: invalid url %q", h.URL)
	}
	q := u.Query()
	for k, v := range h.Query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	var body io.Reader
	contentType := ""
	switch {
	case len(h.Form) > 0:
		f := url.Values{}
		for k, v := range h.Form {
			f.Set(k, v)
		}
		body, contentType = strings.NewReader(f.Encode()), "application/x-www-form-urlencoded"
	case h.Body != nil:
		if str, ok := h.Body.(string); ok {
			body = strings.NewReader(str)
			if json.Valid([]byte(str)) {
				contentType = "application/json"
			}
		} else {
			b, err := json.Marshal(h.Body)
			if err != nil {
				return "", fmt.Errorf("auth call body: %w", err)
			}
			body, contentType = strings.NewReader(string(b)), "application/json"
		}
	}
	method := strings.ToUpper(h.Method)
	if method == "" {
		method = http.MethodPost
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return "", fmt.Errorf("auth call: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range h.Headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if tlsCfg != nil {
		tr.TLSClientConfig = tlsCfg
	}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return "", fmt.Errorf("auth call to %s: %w", h.URL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("auth call to %s answered %d: %s", h.URL, resp.StatusCode, truncate(string(raw), 300))
	}
	path := h.TokenPath
	if path == "" {
		path = "access_token"
	}
	if strings.HasPrefix(path, "$header.") {
		if v := resp.Header.Get(strings.TrimPrefix(path, "$header.")); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("auth call to %s: response has no %s header", h.URL, strings.TrimPrefix(path, "$header."))
	}
	if !json.Valid(raw) {
		return "", fmt.Errorf("auth call to %s did not return JSON", h.URL)
	}
	tok := gjson.GetBytes(raw, path).String()
	if tok == "" {
		return "", fmt.Errorf("auth call to %s: response has no %q value", h.URL, path)
	}
	return tok, nil
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

func loadTLS(c *Credentials, t *model.TLSSettings, certsDir string) error {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.ServerName, InsecureSkipVerify: t.InsecureSkipVerify}
	c.ServerName, c.InsecureSkipVerify = t.ServerName, t.InsecureSkipVerify
	if t.Cert != "" {
		c.CertFile = filepath.Join(certsDir, t.Cert)
		c.KeyFile = filepath.Join(certsDir, t.Key)
		pair, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return fmt.Errorf("client certificate %s / key %s: %w", t.Cert, t.Key, err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	if t.CA != "" {
		c.CAFile = filepath.Join(certsDir, t.CA)
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
	return TLSChecks(t, p.Type == model.AuthMTLS, certsDir)
}

// TLSChecks is CertChecks for bare TLS settings (a certificate set).
func TLSChecks(t *model.TLSSettings, clientRequired bool, certsDir string) []Check {
	mtls := clientRequired || t.Cert != "" || t.Key != ""
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
