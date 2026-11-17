package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testplane/testplane/internal/demo"
	"github.com/testplane/testplane/internal/model"
	"github.com/testplane/testplane/internal/store"
)

func request(t *testing.T, method, url string, headers map[string]string, body any) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func TestAccessTokenAndCORS(t *testing.T) {
	st := store.New(t.TempDir())
	_ = st.SaveApp(&model.Application{ID: "shop", Name: "Shop"})
	s := New(st, nil)
	s.Token = "s3cret"
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	auth := map[string]string{"Authorization": "Bearer s3cret"}

	for name, h := range map[string]map[string]string{"no token": nil, "wrong token": {"Authorization": "Bearer nope"}, "wrong scheme": {"Authorization": "Basic s3cret"}, "token as prefix": {"Authorization": "Bearer s3cret-and-more"}} {
		if resp, _ := request(t, "GET", srv.URL+"/api/apps", h, nil); resp.StatusCode != 401 {
			t.Errorf("%s: %d, want 401", name, resp.StatusCode)
		}
	}
	if resp, _ := request(t, "PUT", srv.URL+"/api/apps/evil", nil, model.Application{Name: "x"}); resp.StatusCode != 401 {
		t.Errorf("write without token: %d", resp.StatusCode)
	}
	if _, err := st.GetApp("evil"); err == nil {
		t.Fatal("an unauthenticated write must not reach the store")
	}
	if resp, _ := request(t, "GET", srv.URL+"/api/apps", auth, nil); resp.StatusCode != 200 {
		t.Errorf("with token: %d", resp.StatusCode)
	}
	resp, raw := request(t, "GET", srv.URL+"/api/meta", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"authRequired":true`) || strings.Contains(string(raw), st.Root) {
		t.Errorf("meta must be open, say a token is needed and not leak the workspace path: %d %s", resp.StatusCode, raw)
	}

	// With a token set, any origin may call (the token is what protects it).
	resp, _ = request(t, "OPTIONS", srv.URL+"/api/apps/shop", map[string]string{"Origin": "https://ui.example.com", "Access-Control-Request-Method": "PUT"}, nil)
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "https://ui.example.com" || !strings.Contains(resp.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Errorf("preflight: %d %v", resp.StatusCode, resp.Header)
	}

	// An explicit list is exact.
	s.AllowOrigins = []string{"https://ui.example.com/"}
	resp, _ = request(t, "GET", srv.URL+"/api/apps", map[string]string{"Origin": "https://other.example.com", "Authorization": "Bearer s3cret"}, nil)
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("origin not on the list must get no CORS header")
	}
	resp, _ = request(t, "GET", srv.URL+"/api/apps", map[string]string{"Origin": "https://ui.example.com", "Authorization": "Bearer s3cret"}, nil)
	if resp.Header.Get("Access-Control-Allow-Origin") != "https://ui.example.com" {
		t.Error("listed origin must be allowed")
	}

	// No token and no list: same-origin only.
	open := New(st, nil)
	osrv := httptest.NewServer(open.Handler())
	defer osrv.Close()
	resp, _ = request(t, "GET", osrv.URL+"/api/apps", map[string]string{"Origin": "https://ui.example.com"}, nil)
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("open backend: %d, CORS %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestSecretsAreWriteOnly(t *testing.T) {
	target := httptest.NewServer(demo.Handler())
	defer target.Close()
	st := store.New(t.TempDir())
	_ = st.SaveApp(&model.Application{ID: "shop", Name: "Shop", DefaultEnvironment: "t", Environments: map[string]map[string]string{"t": {"baseUrl": target.URL}}})
	srv := httptest.NewServer(New(st, nil).Handler())
	defer srv.Close()
	c := client{t, srv.URL}

	profile := model.AuthProfile{Name: "OAuth", Type: model.AuthOAuth2ClientCredentials, OAuth2: &model.OAuth2Auth{TokenURL: "{{baseUrl}}/oauth/token", ClientID: demo.ClientID, ClientSecret: demo.ClientSecret}}
	var saved model.AuthProfile
	if code := c.do("PUT", "/api/global/auth/oauth", profile, &saved); code != 200 || saved.OAuth2.ClientSecret != model.SecretMask {
		t.Fatalf("save response must be masked: %d %+v", code, saved.OAuth2)
	}
	var raw []byte
	c.do("GET", "/api/global/auth", nil, &raw)
	if bytes.Contains(raw, []byte(demo.ClientSecret)) || !bytes.Contains(raw, []byte(model.SecretMask)) {
		t.Fatalf("listing leaks the secret: %s", raw)
	}

	// Saving the masked value back keeps the stored secret, and it still works.
	saved.Name = "OAuth renamed"
	if code := c.do("PUT", "/api/global/auth/oauth", saved, nil); code != 200 {
		t.Fatalf("resave: %d", code)
	}
	stored, _ := st.GetAuth("oauth")
	if stored.OAuth2.ClientSecret != demo.ClientSecret || stored.Name != "OAuth renamed" {
		t.Fatalf("stored after resave: %+v", stored.OAuth2)
	}
	var check struct{ OK bool }
	if c.do("POST", "/api/global/auth/oauth/check?app=shop", nil, &check); !check.OK {
		t.Fatal("profile must still fetch a token after a masked resave")
	}

	// A new value replaces it; an env reference is shown as written.
	saved.OAuth2.ClientSecret = "{{$env.ORDERS_SECRET}}"
	c.do("PUT", "/api/global/auth/oauth", saved, &saved)
	if saved.OAuth2.ClientSecret != "{{$env.ORDERS_SECRET}}" {
		t.Fatalf("env reference should not be masked: %q", saved.OAuth2.ClientSecret)
	}
	if stored, _ = st.GetAuth("oauth"); stored.OAuth2.ClientSecret != "{{$env.ORDERS_SECRET}}" {
		t.Fatalf("replacement not stored: %q", stored.OAuth2.ClientSecret)
	}

	// Every secret field of every type is masked.
	for id, p := range map[string]model.AuthProfile{
		"basic":  {Type: model.AuthBasic, Basic: &model.BasicAuth{Username: "u", Password: "pw-secret"}},
		"bearer": {Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "tok-secret"}},
		"key":    {Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{Name: "X-Key", Value: "key-secret"}},
		"pw":     {Type: model.AuthOAuth2Password, OAuth2: &model.OAuth2Auth{TokenURL: "http://x", ClientID: "c", ClientSecret: "cs-secret", Username: "u", Password: "up-secret"}},
	} {
		c.do("PUT", "/api/global/auth/"+id, p, &raw)
		if bytes.Contains(raw, []byte("-secret")) {
			t.Errorf("%s: save response leaks: %s", id, raw)
		}
	}
	c.do("GET", "/api/global/auth", nil, &raw)
	if bytes.Contains(raw, []byte("-secret")) || !bytes.Contains(raw, []byte(`"username":"u"`)) {
		t.Errorf("listing leaks or hides too much: %s", raw)
	}
	// A masked value for a profile that never had a secret stores nothing.
	c.do("PUT", "/api/global/auth/fresh", model.AuthProfile{Type: model.AuthBasic, Basic: &model.BasicAuth{Username: "u", Password: model.SecretMask}}, nil)
	if p, _ := st.GetAuth("fresh"); p.Basic.Password != "" {
		t.Errorf("mask must never be stored as a password: %q", p.Basic.Password)
	}
	// The export has neither keys nor literal secrets, whatever is asked.
	// It carries the global profiles the application uses.
	shop, _ := st.GetApp("shop")
	shop.DefaultAuth = "basic"
	_ = st.SaveApp(shop)
	c.do("GET", "/api/apps/shop/export?keys=true", nil, &raw)
	if len(raw) == 0 {
		t.Fatal("export failed")
	}
	other := store.New(t.TempDir())
	if _, err := other.ImportApp(raw); err != nil {
		t.Fatal(err)
	}
	if p, _ := other.GetAuth("basic"); p.Basic.Password != "CHANGE_ME" {
		t.Errorf("exported password: %q", p.Basic.Password)
	}
}

func TestHistoryPublishAndRetention(t *testing.T) {
	target := httptest.NewServer(demo.Handler())
	defer target.Close()
	st := store.New(t.TempDir())
	_ = st.SaveApp(&model.Application{ID: "shop", Name: "Shop", DefaultEnvironment: "t", Environments: map[string]map[string]string{"t": {"baseUrl": target.URL}}})
	_ = st.SaveScenario("shop", &model.Scenario{ID: "up", Name: "Up", Tests: []model.Test{{ID: "h", Name: "Health", Type: "http", HTTP: &model.HTTPStep{URL: "{{baseUrl}}/health"}}}})
	_ = st.SaveScenario("shop", &model.Scenario{ID: "down", Name: "Down", Tests: []model.Test{{ID: "h", Name: "Missing", Type: "http", HTTP: &model.HTTPStep{URL: "{{baseUrl}}/nope"}}}})
	s := New(st, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := client{t, srv.URL}

	resp, raw := request(t, "POST", srv.URL+"/api/apps/shop/run", map[string]string{"X-Testplane-User": "  anudeep  "}, map[string]any{})
	var run model.RunResult
	_ = json.Unmarshal(raw, &run)
	if resp.StatusCode != 200 || run.TriggeredBy != "anudeep" || run.Source != "server" || run.Status != model.StatusFailed || run.StartedAt.IsZero() {
		t.Fatalf("run: %d %+v", resp.StatusCode, run)
	}

	var list []model.RunResult
	if c.do("GET", "/api/apps/shop/runs?scenario=up", nil, &list); len(list) != 1 || list[0].Status != model.StatusPassed || list[0].TriggeredBy != "anudeep" {
		t.Fatalf("scenario history: %+v", list)
	}
	if c.do("GET", "/api/apps/shop/runs?scenario=down&status=passed", nil, &list); len(list) != 0 {
		t.Fatalf("status filter: %+v", list)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if c.do("GET", "/api/apps/shop/runs?from="+today+"&to="+today, nil, &list); len(list) != 1 {
		t.Fatalf("a plain date range must cover the whole day: %+v", list)
	}
	if c.do("GET", "/api/apps/shop/runs?to=2020-01-01", nil, &list); len(list) != 0 {
		t.Fatalf("old window: %+v", list)
	}
	if code := c.do("GET", "/api/apps/shop/runs?from=yesterday", nil, nil); code != 400 {
		t.Fatalf("bad date: %d", code)
	}

	// A pipeline publishes a result it produced elsewhere.
	old := model.RunResult{ID: "20200101-000000-ci", Application: "shop", Environment: "t", StartedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Status: model.StatusPassed,
		Source: "cli", TriggeredBy: "ci", Scenarios: []model.ScenarioResult{{ID: "up", Status: model.StatusPassed, Tests: []model.TestResult{{ID: "h", Status: model.StatusPassed}}}}}
	if code := c.do("POST", "/api/apps/shop/runs", old, nil); code != 201 {
		t.Fatalf("publish: %d", code)
	}
	if c.do("GET", "/api/apps/shop/runs?scenario=up", nil, &list); len(list) != 2 || list[1].Source != "cli" {
		t.Fatalf("published run should be in the history: %+v", list)
	}
	for name, bad := range map[string]any{
		"wrong app":    model.RunResult{ID: "x", Application: "other", StartedAt: time.Now(), Scenarios: []model.ScenarioResult{}},
		"path in id":   model.RunResult{ID: "../x", Application: "shop", StartedAt: time.Now(), Scenarios: []model.ScenarioResult{}},
		"not a result": map[string]string{"application": "shop", "id": "x"},
	} {
		if code := c.do("POST", "/api/apps/shop/runs", bad, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	if code := c.do("POST", "/api/apps/ghost/runs", model.RunResult{ID: "x", Application: "ghost", StartedAt: time.Now(), Scenarios: []model.ScenarioResult{}}, nil); code != 404 {
		t.Errorf("publish to unknown app: %d", code)
	}

	// Retention removes the 2020 run the next time a run is stored.
	s.RetentionDays = 30
	c.do("POST", "/api/apps/shop/run", map[string]any{"scenarios": []string{"up"}}, nil)
	if c.do("GET", "/api/apps/shop/runs", nil, &list); len(list) != 2 {
		t.Fatalf("after retention: %d runs", len(list))
	}
	if _, err := os.Stat(filepath.Join(st.Root, ".runs", "shop", "20200101-000000-ci.json")); !os.IsNotExist(err) {
		t.Error("old run file should be gone")
	}
}

func TestCertificateSetsOverHTTP(t *testing.T) {
	st := store.New(t.TempDir())
	_ = st.SaveApp(&model.Application{ID: "shop", Name: "Shop", DefaultEnvironment: "dev", Environments: map[string]map[string]string{"dev": {}, "staging": {}}})
	certs, _ := demo.NewCerts()
	_ = certs.WriteClientFiles(st.CertsDir())
	srv := httptest.NewServer(New(st, nil).Handler())
	defer srv.Close()
	c := client{t, srv.URL}

	type view struct {
		ID     string
		Cert   string
		Checks []struct{ Field, Status, File string }
	}
	set := model.CertSet{Name: "Client", Cert: "demo-client.crt", Key: "demo-client.key", CA: "demo-ca.crt",
		Environments: map[string]model.TLSSettings{"staging": {Cert: "staging.crt", Key: "staging.key"}}}
	var saved view
	if code := c.do("PUT", "/api/global/tls/client", set, &saved); code != 200 || len(saved.Checks) != 3 || saved.Checks[0].Status != "ok" {
		t.Fatalf("save: %d %+v", code, saved)
	}
	var list []view
	if c.do("GET", "/api/global/tls", nil, &list); len(list) != 1 || list[0].Checks[0].Status != "ok" || list[0].Checks[0].File != "demo-client.crt" {
		t.Fatalf("default environment: %+v", list)
	}
	if c.do("GET", "/api/global/tls?env=staging", nil, &list); list[0].Checks[0].Status != "missing" || list[0].Checks[0].File != "staging.crt" || list[0].Checks[2].Status != "ok" {
		t.Fatalf("staging should report its own missing files and the shared CA: %+v", list)
	}
	for name, bad := range map[string]model.CertSet{
		"empty":            {Name: "x"},
		"cert without key": {Cert: "demo-client.crt"},
		"path":             {Cert: "../x.crt", Key: "x.key"},
	} {
		if code := c.do("PUT", "/api/global/tls/bad", bad, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	if code := c.do("PUT", "/api/global/tls/Bad%20Id", set, nil); code != 400 {
		t.Errorf("bad id: %d", code)
	}

	// A scenario can name the set; the zip carries it; delete removes it.
	sc := model.Scenario{Name: "S", MTLS: "client", Tests: []model.Test{{ID: "t", Name: "T", Type: "http", MTLS: "none", HTTP: &model.HTTPStep{URL: "http://x"}}}}
	if code := c.do("PUT", "/api/apps/shop/scenarios/s", sc, nil); code != 200 {
		t.Fatalf("scenario with mtls: %d", code)
	}
	var raw []byte
	c.do("GET", "/api/apps/shop/scenarios/s/yaml", nil, &raw)
	if !strings.Contains(string(raw), "mtls: client") || !strings.Contains(string(raw), "mtls: none") {
		t.Errorf("yaml: %s", raw)
	}
	c.do("GET", "/api/apps/shop/export", nil, &raw)
	other := store.New(t.TempDir())
	if _, err := other.ImportApp(raw); err != nil {
		t.Fatal(err)
	}
	if got, err := other.GetCertSet("client"); err != nil || got.Environments["staging"].Cert != "staging.crt" {
		t.Errorf("certificate set after import: %+v %v", got, err)
	}
	if code := c.do("DELETE", "/api/global/tls/client", nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code := c.do("DELETE", "/api/global/tls/client", nil, nil); code != 404 {
		t.Errorf("second delete: %d", code)
	}
}
