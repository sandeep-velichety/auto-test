package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/demo"
	"github.com/testplane/testplane/internal/model"
)

func TestStaticCredentialTypes(t *testing.T) {
	r := NewResolver(t.TempDir())
	ctx := context.Background()
	c, err := r.Resolve(ctx, &model.AuthProfile{ID: "b", Type: model.AuthBasic, Basic: &model.BasicAuth{Username: "u", Password: "p"}})
	if err != nil || c.Headers["Authorization"] != "Basic dTpw" || c.Username != "u" || c.Password != "p" {
		t.Fatalf("basic: %+v %v", c, err)
	}
	c, _ = r.Resolve(ctx, &model.AuthProfile{ID: "t", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "abc"}})
	if c.Headers["Authorization"] != "Bearer abc" {
		t.Fatalf("bearer: %v", c.Headers)
	}
	c, _ = r.Resolve(ctx, &model.AuthProfile{ID: "t2", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "abc", Prefix: "none"}})
	if c.Headers["Authorization"] != "abc" {
		t.Fatalf("bearer without prefix: %v", c.Headers)
	}
	c, _ = r.Resolve(ctx, &model.AuthProfile{ID: "k", Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{Name: "X-Api-Key", Value: "v"}})
	if c.Headers["X-Api-Key"] != "v" || len(c.Query) != 0 {
		t.Fatalf("api key header: %+v", c)
	}
	c, _ = r.Resolve(ctx, &model.AuthProfile{ID: "q", Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{Name: "key", Value: "v", In: "query"}})
	if c.Query["key"] != "v" || len(c.Headers) != 0 {
		t.Fatalf("api key query: %+v", c)
	}
	c, err = r.Resolve(ctx, nil)
	if err != nil || len(c.Headers) != 0 || c.TLS != nil {
		t.Fatalf("nil profile: %+v %v", c, err)
	}
	if _, err := r.Resolve(ctx, &model.AuthProfile{ID: "x", Type: model.AuthBearer}); err == nil {
		t.Fatal("bearer without a token must fail")
	}
	if _, err := r.Resolve(ctx, &model.AuthProfile{ID: "y", Type: "kerberos"}); err == nil {
		t.Fatal("unknown type must fail")
	}
}

func TestOAuth2TokenIsFetchedOnceAndSentAsConfigured(t *testing.T) {
	calls := 0
	var form map[string]string
	var basicUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseForm()
		form = map[string]string{}
		for k := range r.Form {
			form[k] = r.Form.Get(k)
		}
		basicUser, _, _ = r.BasicAuth()
		if r.Form.Get("fail") != "" {
			http.Error(w, `{"error":"invalid_client"}`, 401)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"jwt":"tok-123"},"access_token":"plain"}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	r := NewResolver(t.TempDir())

	p := &model.AuthProfile{ID: "cc", Type: model.AuthOAuth2ClientCredentials, OAuth2: &model.OAuth2Auth{
		TokenURL: srv.URL, ClientID: "id", ClientSecret: "sec", Scopes: []string{"a", "b"}, Audience: "aud", ExtraParams: map[string]string{"resource": "r"}}}
	c, err := r.Resolve(ctx, p)
	if err != nil || c.Headers["Authorization"] != "Bearer plain" {
		t.Fatalf("client credentials: %+v %v", c, err)
	}
	want := map[string]string{"grant_type": "client_credentials", "client_id": "id", "client_secret": "sec", "scope": "a b", "audience": "aud", "resource": "r"}
	for k, v := range want {
		if form[k] != v {
			t.Errorf("form %s=%q, want %q", k, form[k], v)
		}
	}
	if _, err := r.Resolve(ctx, p); err != nil || calls != 1 {
		t.Fatalf("token must be cached within a run: calls=%d err=%v", calls, err)
	}

	c, err = r.Resolve(ctx, &model.AuthProfile{ID: "pw", Type: model.AuthOAuth2Password, OAuth2: &model.OAuth2Auth{
		TokenURL: srv.URL, ClientID: "id", ClientSecret: "sec", Username: "u", Password: "p", ClientAuth: "basic", TokenField: "data.jwt", HeaderName: "X-Token", Prefix: "none"}})
	if err != nil || c.Headers["X-Token"] != "tok-123" {
		t.Fatalf("password grant: %+v %v", c, err)
	}
	if form["grant_type"] != "password" || form["username"] != "u" || form["client_id"] != "" || basicUser != "id" {
		t.Errorf("password grant request: form=%v basic=%q", form, basicUser)
	}

	_, err = r.Resolve(ctx, &model.AuthProfile{ID: "bad", Type: model.AuthOAuth2ClientCredentials, OAuth2: &model.OAuth2Auth{TokenURL: srv.URL, ClientID: "id", ExtraParams: map[string]string{"fail": "1"}}})
	if err == nil || !strings.Contains(err.Error(), "answered 401") {
		t.Fatalf("rejected client: %v", err)
	}
	_, err = r.Resolve(ctx, &model.AuthProfile{ID: "nofield", Type: model.AuthOAuth2ClientCredentials, OAuth2: &model.OAuth2Auth{TokenURL: srv.URL, ClientID: "id", TokenField: "missing"}})
	if err == nil || !strings.Contains(err.Error(), `no "missing" field`) {
		t.Fatalf("missing token field: %v", err)
	}
}

func TestCertChecksSayWhatIsMissing(t *testing.T) {
	dir := t.TempDir()
	status := func(p *model.AuthProfile) map[string]string {
		out := map[string]string{}
		for _, c := range CertChecks(p, dir) {
			out[c.Field] = c.Status
			if c.Field != "tls.ca" && !c.Required {
				t.Errorf("%s should be required for mTLS", c.Field)
			}
		}
		return out
	}
	if CertChecks(&model.AuthProfile{Type: model.AuthBearer}, dir) != nil {
		t.Fatal("no TLS, no checks")
	}
	got := status(&model.AuthProfile{Type: model.AuthMTLS})
	if got["tls.cert"] != "not_set" || got["tls.key"] != "not_set" || got["tls.ca"] != "not_set" {
		t.Fatalf("empty mtls profile: %v", got)
	}
	p := &model.AuthProfile{ID: "m", Type: model.AuthMTLS, TLS: &model.TLSSettings{Cert: "demo-client.crt", Key: "demo-client.key", CA: "demo-ca.crt"}}
	if got = status(p); got["tls.cert"] != "missing" || got["tls.key"] != "missing" {
		t.Fatalf("files not uploaded: %v", got)
	}
	certs, err := demo.NewCerts()
	if err != nil {
		t.Fatal(err)
	}
	if err := certs.WriteClientFiles(dir); err != nil {
		t.Fatal(err)
	}
	if got = status(p); got["tls.cert"] != "ok" || got["tls.key"] != "ok" || got["tls.ca"] != "ok" {
		t.Fatalf("all present: %v", got)
	}
	c, err := NewResolver(dir).Resolve(context.Background(), p)
	if err != nil || c.TLS == nil || len(c.TLS.Certificates) != 1 || c.TLS.RootCAs == nil || c.CertFile != filepath.Join(dir, "demo-client.crt") {
		t.Fatalf("resolve mtls: %+v %v", c, err)
	}

	// Swapped files: the certificate where the key should be.
	swapped := &model.AuthProfile{ID: "s", Type: model.AuthMTLS, TLS: &model.TLSSettings{Cert: "demo-client.key", Key: "demo-client.crt"}}
	if got = status(swapped); got["tls.cert"] != "wrong_kind" || got["tls.key"] != "wrong_kind" {
		t.Fatalf("swapped files: %v", got)
	}
	// A key from a different pair.
	other, _ := demo.NewCerts()
	_ = os.WriteFile(filepath.Join(dir, "other.key"), other.ClientKey, 0o600)
	mismatch := &model.AuthProfile{ID: "x", Type: model.AuthMTLS, TLS: &model.TLSSettings{Cert: "demo-client.crt", Key: "other.key"}}
	if got = status(mismatch); got["tls.key"] != "mismatch" {
		t.Fatalf("mismatched key: %v", got)
	}
	if _, err := NewResolver(dir).Resolve(context.Background(), mismatch); err == nil {
		t.Fatal("a mismatched key pair must not resolve")
	}
}
