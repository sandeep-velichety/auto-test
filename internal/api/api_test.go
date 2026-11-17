package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/testplane/testplane/internal/demo"
	"github.com/testplane/testplane/internal/engine"
	"github.com/testplane/testplane/internal/model"
	"github.com/testplane/testplane/internal/store"
)

type client struct {
	t    *testing.T
	base string
}

func (c client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	switch o := out.(type) {
	case nil:
	case *[]byte:
		*o = raw
	default:
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode
}

func TestWorkspaceLifecycleOverHTTP(t *testing.T) {
	target := httptest.NewServer(demo.Handler())
	defer target.Close()
	ui := fstest.MapFS{"index.html": {Data: []byte("<html>ui</html>")}, "assets/app.js": {Data: []byte("js")}}
	srv := httptest.NewServer(New(store.New(t.TempDir()), ui).Handler())
	defer srv.Close()
	c := client{t, srv.URL}

	var apps []model.Application
	if code := c.do("GET", "/api/apps", nil, &apps); code != 200 || len(apps) != 0 {
		t.Fatalf("empty workspace: %d %v", code, apps)
	}
	app := model.Application{Name: "Shop", DefaultEnvironment: "test", DefaultAuth: "oauth", Environments: map[string]map[string]string{"test": {"baseUrl": target.URL}}}
	if code := c.do("PUT", "/api/apps/shop", app, nil); code != 200 {
		t.Fatalf("create app: %d", code)
	}
	if code := c.do("PUT", "/api/apps/Bad%20Id", app, nil); code != 400 {
		t.Fatalf("invalid app id: %d", code)
	}
	if code := c.do("PUT", "/api/apps/shop", `{"name":"x","nmae":"typo"}`, nil); code != 400 {
		t.Fatalf("unknown JSON field should be refused: %d", code)
	}

	// auth profiles and certificate requirements
	oauth := model.AuthProfile{Type: model.AuthOAuth2ClientCredentials, OAuth2: &model.OAuth2Auth{TokenURL: "{{baseUrl}}/oauth/token", ClientID: demo.ClientID, ClientSecret: demo.ClientSecret}}
	if code := c.do("PUT", "/api/global/auth/oauth", oauth, nil); code != 200 {
		t.Fatalf("save oauth: %d", code)
	}
	var saved struct {
		ID         string `json:"id"`
		CertChecks []struct{ Field, Status string }
	}
	mtls := model.AuthProfile{Type: model.AuthMTLS, TLS: &model.TLSSettings{Cert: "c.crt", Key: "c.key"}}
	if code := c.do("PUT", "/api/global/auth/mtls", mtls, &saved); code != 200 || len(saved.CertChecks) != 3 || saved.CertChecks[0].Status != "missing" {
		t.Fatalf("save mtls: %d %+v", code, saved)
	}
	if code := c.do("PUT", "/api/global/auth/broken", model.AuthProfile{Type: model.AuthMTLS}, nil); code != 400 {
		t.Fatalf("mtls without files: %d", code)
	}
	certs, _ := demo.NewCerts()
	if code := c.do("PUT", "/api/global/certs/c.crt", certs.ClientCert, nil); code != 204 {
		t.Fatalf("upload cert: %d", code)
	}
	if code := c.do("PUT", "/api/global/certs/c.key", certs.ClientKey, nil); code != 204 {
		t.Fatalf("upload key: %d", code)
	}
	if code := c.do("PUT", "/api/global/certs/..%2Fescape", "x", nil); code != 400 {
		t.Fatalf("path escape in asset name: %d", code)
	}
	var check struct {
		OK         bool
		Error      string
		ClientCert bool
		Sends      []string
	}
	if code := c.do("POST", "/api/global/auth/mtls/check?app=shop", nil, &check); code != 200 || !check.OK || !check.ClientCert {
		t.Fatalf("check mtls: %d %+v", code, check)
	}
	if code := c.do("POST", "/api/global/auth/oauth/check?app=shop", nil, &check); code != 200 || !check.OK || len(check.Sends) != 1 {
		t.Fatalf("check oauth: %d %+v", code, check)
	}
	var assets []store.Asset
	if code := c.do("GET", "/api/global/certs", nil, &assets); code != 200 || len(assets) != 2 {
		t.Fatalf("assets: %d %+v", code, assets)
	}
	var raw []byte
	c.do("GET", "/api/global/certs", nil, &raw)
	if bytes.Contains(raw, []byte("PRIVATE KEY")) {
		t.Fatal("asset listing must never contain key material")
	}

	// scenarios, structured and as YAML
	sc := model.Scenario{Name: "Orders", Tests: []model.Test{
		{ID: "create", Name: "Create", Type: "http", HTTP: &model.HTTPStep{Method: "POST", URL: "{{baseUrl}}/orders", Body: map[string]any{"item": "x", "quantity": 1}},
			Capture: map[string]string{"id": "id"}, Variants: []model.Variant{{ID: "anon", Name: "Anonymous", Auth: "none"}}},
		{ID: "read", Name: "Read", Type: "http", HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/{{id}}"}},
	}}
	if code := c.do("PUT", "/api/apps/shop/scenarios/orders", sc, nil); code != 200 {
		t.Fatalf("save scenario: %d", code)
	}
	if code := c.do("PUT", "/api/apps/shop/scenarios/bad", model.Scenario{Name: "Bad", Tests: []model.Test{{ID: "x", Type: "ftp"}}}, nil); code != 400 {
		t.Fatalf("invalid scenario: %d", code)
	}
	c.do("GET", "/api/apps/shop/scenarios/orders/yaml", nil, &raw)
	if !bytes.Contains(raw, []byte("variants:")) {
		t.Fatalf("yaml: %s", raw)
	}
	edited := bytes.Replace(raw, []byte("name: Orders"), []byte("name: Orders v2"), 1)
	var back model.Scenario
	if code := c.do("PUT", "/api/apps/shop/scenarios/orders/yaml", edited, &back); code != 200 || back.Name != "Orders v2" {
		t.Fatalf("save yaml: %d %+v", code, back)
	}
	if code := c.do("PUT", "/api/apps/shop/scenarios/orders/yaml", "tests: [", nil); code != 400 {
		t.Fatalf("broken yaml: %d", code)
	}
	if code := c.do("GET", "/api/apps/shop/scenarios/ghost", nil, nil); code != 404 {
		t.Fatalf("missing scenario: %d", code)
	}

	// run and history
	var run model.RunResult
	if code := c.do("POST", "/api/apps/shop/run", map[string]any{"scenarios": []string{"orders"}}, &run); code != 200 || run.Status != model.StatusPassed || run.Summary.Total != 3 {
		t.Fatalf("run: %d %+v", code, run)
	}
	if code := c.do("POST", "/api/apps/shop/run", map[string]any{"kind": "sideways"}, nil); code != 400 {
		t.Fatalf("bad kind: %d", code)
	}
	if code := c.do("POST", "/api/apps/shop/run", map[string]any{"scenarios": []string{"ghost"}}, nil); code != 404 {
		t.Fatalf("run unknown scenario: %d", code)
	}
	if code := c.do("POST", "/api/apps/shop/run", map[string]any{"environment": "ghost"}, nil); code != 400 {
		t.Fatalf("run unknown environment: %d", code)
	}
	var runs []model.RunResult
	if code := c.do("GET", "/api/apps/shop/runs", nil, &runs); code != 200 || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("runs: %d %+v", code, runs)
	}
	var again model.RunResult
	if code := c.do("GET", "/api/apps/shop/runs/"+run.ID, nil, &again); code != 200 || len(again.Scenarios[0].Tests) != 3 {
		t.Fatalf("run detail: %d", code)
	}

	// export, delete, import
	var zipped []byte
	if code := c.do("GET", "/api/apps/shop/export", nil, &zipped); code != 200 || len(zipped) == 0 {
		t.Fatalf("export: %d", code)
	}
	if code := c.do("DELETE", "/api/apps/shop", nil, nil); code != 204 {
		t.Fatalf("delete app: %d", code)
	}
	if code := c.do("GET", "/api/apps/shop", nil, nil); code != 404 {
		t.Fatalf("deleted app: %d", code)
	}
	var imported map[string]string
	if code := c.do("POST", "/api/import", zipped, &imported); code != 200 || imported["id"] != "shop" {
		t.Fatalf("import: %d %v", code, imported)
	}
	if code := c.do("GET", "/api/apps/shop/scenarios/orders", nil, &back); code != 200 || back.Name != "Orders v2" {
		t.Fatalf("scenario after import: %d", code)
	}

	// UI: files are served, client-side routes fall back to index.html, unknown API paths do not.
	c.do("GET", "/assets/app.js", nil, &raw)
	if string(raw) != "js" {
		t.Fatalf("static file: %q", raw)
	}
	c.do("GET", "/apps/shop/scenarios/orders", nil, &raw)
	if string(raw) != "<html>ui</html>" {
		t.Fatalf("spa fallback: %q", raw)
	}
	if code := c.do("GET", "/api/nope", nil, nil); code != 404 {
		t.Fatalf("unknown api path: %d", code)
	}
}

// Environments, auth and certificates are global: one set of endpoints
// serves every application.
func TestGlobalSettingsUsageAndProtosOverHTTP(t *testing.T) {
	st := store.New(t.TempDir())
	srv := httptest.NewServer(New(st, nil).Handler())
	defer srv.Close()
	c := client{t, srv.URL}

	var g struct {
		model.Global
		Keys []string `json:"keys"`
	}
	if code := c.do("GET", "/api/global", nil, &g); code != 200 || len(g.Keys) != 6 || g.Keys[0] != "stage/NA" {
		t.Fatalf("a new workspace starts with the standard environments and regions: %d %+v", code, g)
	}
	if code := c.do("PUT", "/api/global", model.Global{Environments: []string{"stage"}, Regions: []string{"stage"}}, nil); code != 400 {
		t.Fatalf("invalid list: %d", code)
	}
	next := model.Global{Environments: []string{"stage", "prod"}, Regions: []string{"NA", "AU", "EU"}, DefaultEnvironment: "stage/EU", Values: map[string]map[string]string{"stage/EU": {"idpUrl": "http://idp"}}}
	if code := c.do("PUT", "/api/global", next, &g); code != 200 || len(g.Keys) != 6 || g.DefaultEnvironment != "stage/EU" {
		t.Fatalf("save: %d %+v", code, g)
	}

	// Two applications use the same profile and certificate set.
	for _, id := range []string{"orders", "billing"} {
		if code := c.do("PUT", "/api/apps/"+id, model.Application{Name: id, DefaultAuth: "key"}, nil); code != 200 {
			t.Fatalf("create %s: %d", id, code)
		}
	}
	if code := c.do("PUT", "/api/global/auth/key", model.AuthProfile{Name: "Company key", Type: model.AuthHeaders, Headers: map[string]string{"X-Key": "k"},
		Environments: map[string]map[string]any{"stage/EU": {"headers": map[string]any{"X-Key": "eu"}}}}, nil); code != 200 {
		t.Fatalf("save global auth: %d", code)
	}
	if code := c.do("PUT", "/api/global/tls/client", model.CertSet{Name: "Client", CA: "ca.pem"}, nil); code != 200 {
		t.Fatalf("save global certificate set: %d", code)
	}
	sc := model.Scenario{Name: "S", MTLS: "client", Tests: []model.Test{{ID: "t", Name: "T", Type: "http", Auth: "key", HTTP: &model.HTTPStep{URL: "http://x"}}}}
	c.do("PUT", "/api/apps/orders/scenarios/one", sc, nil)
	c.do("PUT", "/api/apps/billing/scenarios/two", sc, nil)
	var usage store.Usage
	if code := c.do("GET", "/api/global/usage", nil, &usage); code != 200 || len(usage.Auth["key"]) != 4 || len(usage.CertSets["client"]) != 2 {
		t.Fatalf("usage: %d %+v", code, usage)
	}
	var check engine.AuthCheck
	if code := c.do("POST", "/api/global/auth/key/check", nil, &check); code != 200 || !check.OK {
		t.Fatalf("a global profile can be tried without an application: %d %+v", code, check)
	}

	// The per-application auth endpoints are gone; protos stay with the application.
	var raw []byte
	if code := c.do("GET", "/api/apps/orders/auth", nil, &raw); code != 404 {
		t.Errorf("old endpoint: %d", code)
	}
	if code := c.do("PUT", "/api/apps/orders/protos/orders.proto", `syntax = "proto3";`, nil); code != 204 {
		t.Fatalf("upload proto: %d", code)
	}
	var protos []store.Asset
	if code := c.do("GET", "/api/apps/orders/protos", nil, &protos); code != 200 || len(protos) != 1 || protos[0].Kind != "proto" {
		t.Fatalf("protos: %d %+v", code, protos)
	}
	if c.do("GET", "/api/apps/billing/protos", nil, &protos); len(protos) != 0 {
		t.Errorf("protos belong to one application: %+v", protos)
	}
	if code := c.do("DELETE", "/api/apps/orders/protos/orders.proto", nil, nil); code != 204 {
		t.Errorf("delete proto: %d", code)
	}
}

// A name that is taken answers 409 and says which item has it, so a client
// can offer to update that one.
func TestDuplicateNamesAnswerConflictOverHTTP(t *testing.T) {
	st := store.New(t.TempDir())
	srv := httptest.NewServer(New(st, nil).Handler())
	defer srv.Close()
	c := client{t, srv.URL}
	var conflict struct{ Error, Kind, Name, Existing string }

	c.do("PUT", "/api/apps/orders", model.Application{Name: "Orders"}, nil)
	if code := c.do("PUT", "/api/apps/orders-2", model.Application{Name: "Orders"}, &conflict); code != 409 || conflict.Kind != "application" || conflict.Existing != "orders" {
		t.Fatalf("second application named Orders: %d %+v", code, conflict)
	}
	if code := c.do("PUT", "/api/apps/orders", model.Application{Name: "Orders", Description: "updated"}, nil); code != 200 {
		t.Fatalf("update: %d", code)
	}

	sc := model.Scenario{Name: "Checkout", Tests: []model.Test{{ID: "t", Name: "T", Type: "http", HTTP: &model.HTTPStep{URL: "http://x"}}}}
	c.do("PUT", "/api/apps/orders/scenarios/checkout", sc, nil)
	if code := c.do("PUT", "/api/apps/orders/scenarios/checkout-2", sc, &conflict); code != 409 || conflict.Kind != "scenario" || conflict.Existing != "checkout" {
		t.Fatalf("second scenario named Checkout: %d %+v", code, conflict)
	}
	sc.Tests = append(sc.Tests, model.Test{ID: "u", Name: "T", Type: "http", HTTP: &model.HTTPStep{URL: "http://x"}})
	if code := c.do("PUT", "/api/apps/orders/scenarios/checkout", sc, &conflict); code != 400 || !strings.Contains(conflict.Error, "unique within a scenario") {
		t.Fatalf("two tests named T: %d %+v", code, conflict)
	}

	key := model.AuthProfile{Name: "Company key", Type: model.AuthHeaders, Headers: map[string]string{"X-Key": "k"}}
	c.do("PUT", "/api/global/auth/company-key", key, nil)
	if code := c.do("PUT", "/api/global/auth/company-key-2", key, &conflict); code != 409 || conflict.Existing != "company-key" {
		t.Fatalf("second auth profile named Company key: %d %+v", code, conflict)
	}
	c.do("PUT", "/api/global/tls/client", model.CertSet{Name: "Client", CA: "ca.pem"}, nil)
	if code := c.do("PUT", "/api/global/tls/client-2", model.CertSet{Name: "Client", CA: "ca.pem"}, &conflict); code != 409 || conflict.Existing != "client" {
		t.Fatalf("second certificate set named Client: %d %+v", code, conflict)
	}

	if code := c.do("PUT", "/api/global/certs/ca.pem", "one", nil); code != 204 {
		t.Fatalf("upload: %d", code)
	}
	if code := c.do("PUT", "/api/global/certs/ca.pem", "two", &conflict); code != 409 || conflict.Kind != "certificate file" {
		t.Fatalf("second upload of ca.pem: %d %+v", code, conflict)
	}
	if code := c.do("PUT", "/api/global/certs/ca.pem?replace=true", "two", nil); code != 204 {
		t.Fatalf("confirmed replacement: %d", code)
	}

	var zip []byte
	c.do("GET", "/api/apps/orders/export", nil, &zip)
	if code := c.do("POST", "/api/import", zip, &conflict); code != 409 || conflict.Existing != "orders" {
		t.Fatalf("importing an application that exists: %d %+v", code, conflict)
	}
	if code := c.do("POST", "/api/import?replace=true", zip, nil); code != 200 {
		t.Fatalf("importing it as an update: %d", code)
	}
}
