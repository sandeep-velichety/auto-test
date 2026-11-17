package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/demo"
	"github.com/testplane/testplane/internal/model"
	"github.com/testplane/testplane/internal/store"
)

// fixture starts the demo system on random ports and builds a workspace
// that points at it.
func fixture(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	api := httptest.NewServer(demo.Handler())
	t.Cleanup(api.Close)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := demo.GRPCServer()
	go gs.Serve(lis)
	t.Cleanup(gs.Stop)

	certs, err := demo.NewCerts()
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg, err := certs.ServerTLS()
	if err != nil {
		t.Fatal(err)
	}
	secure := httptest.NewUnstartedServer(demo.SecureHandler())
	secure.TLS = tlsCfg
	secure.StartTLS()
	t.Cleanup(secure.Close)

	st := store.New(t.TempDir())
	app := &model.Application{ID: "shop", Name: "Shop", DefaultEnvironment: "test", DefaultAuth: "oauth",
		Environments: map[string]map[string]string{
			"test":  {"baseUrl": api.URL, "grpcTarget": lis.Addr().String(), "secureUrl": secure.URL},
			"other": {"baseUrl": "http://127.0.0.1:1"},
		}}
	must(t, st.SaveApp(app))
	must(t, certs.WriteClientFiles(st.AssetDir("shop", "certs")))
	must(t, st.SaveAuth("shop", &model.AuthProfile{ID: "oauth", Type: model.AuthOAuth2ClientCredentials,
		OAuth2: &model.OAuth2Auth{TokenURL: "{{baseUrl}}/oauth/token", ClientID: demo.ClientID, ClientSecret: demo.ClientSecret}}))
	must(t, st.SaveAuth("shop", &model.AuthProfile{ID: "bad-secret", Type: model.AuthOAuth2ClientCredentials,
		OAuth2: &model.OAuth2Auth{TokenURL: "{{baseUrl}}/oauth/token", ClientID: demo.ClientID, ClientSecret: "nope"}}))
	must(t, st.SaveAuth("shop", &model.AuthProfile{ID: "stale", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "stale"}}))
	must(t, st.SaveAuth("shop", &model.AuthProfile{ID: "mtls", Type: model.AuthMTLS,
		TLS: &model.TLSSettings{Cert: "demo-client.crt", Key: "demo-client.key", CA: "demo-ca.crt"}}))
	must(t, st.SaveAuth("shop", &model.AuthProfile{ID: "ca-only", Type: model.AuthNone, TLS: &model.TLSSettings{CA: "demo-ca.crt"}}))
	return New(st), st
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func intp(i int) *int { return &i }

func save(t *testing.T, st *store.Store, sc *model.Scenario) {
	t.Helper()
	if sc.Name == "" {
		sc.Name = sc.ID
	}
	for i := range sc.Tests {
		if sc.Tests[i].Name == "" {
			sc.Tests[i].Name = sc.Tests[i].ID
		}
	}
	must(t, st.SaveScenario("shop", sc))
}

func statuses(r *model.RunResult) map[string]string {
	out := map[string]string{}
	for _, sc := range r.Scenarios {
		for _, tr := range sc.Tests {
			out[tr.ID] = tr.Status
		}
	}
	return out
}

func find(t *testing.T, r *model.RunResult, id string) model.TestResult {
	t.Helper()
	for _, sc := range r.Scenarios {
		for _, tr := range sc.Tests {
			if tr.ID == id {
				return tr
			}
		}
	}
	t.Fatalf("no result for %s", id)
	return model.TestResult{}
}

func lifecycle() *model.Scenario {
	return &model.Scenario{ID: "lifecycle", Variables: map[string]string{"item": "widget"}, Tests: []model.Test{
		{ID: "create", Type: model.TypeHTTP, Properties: map[string]string{"priority": "high"},
			HTTP:    &model.HTTPStep{Method: "POST", URL: "{{baseUrl}}/orders", Body: map[string]any{"item": "{{item}}", "quantity": 2}},
			Expect:  &model.Expect{Status: intp(201), Body: []model.Assertion{{Path: "item", Value: "{{item}}"}}},
			Capture: map[string]string{"orderId": "id", "where": "$header.Location"},
			Variants: []model.Variant{
				{ID: "no-token", Auth: "none", Expect: &model.Expect{Status: intp(401)}},
				{ID: "stale-token", Auth: "stale"},
				{ID: "zero", HTTP: map[string]any{"body": map[string]any{"quantity": 0}}, Expect: &model.Expect{Status: intp(400), Body: []model.Assertion{{Path: "error", Op: "contains", Value: "quantity"}}}},
				{ID: "also-ok", Kind: model.KindPositive, Properties: map[string]string{"priority": "low"}, HTTP: map[string]any{"body": map[string]any{"quantity": 9}}, Expect: &model.Expect{Body: []model.Assertion{{Path: "quantity", Value: 9}}}},
			}},
		{ID: "read", Type: model.TypeHTTP, HTTP: &model.HTTPStep{Method: "GET", URL: "{{baseUrl}}{{where}}"},
			Expect: &model.Expect{Body: []model.Assertion{{Path: "id", Value: "{{orderId}}"}, {Path: "quantity", Value: 2}}}},
		{ID: "delete", Type: model.TypeHTTP, HTTP: &model.HTTPStep{Method: "DELETE", URL: "{{baseUrl}}/orders/{{orderId}}"}, Expect: &model.Expect{Status: intp(204)}},
		{ID: "gone", Type: model.TypeHTTP, Kind: model.KindNegative, HTTP: &model.HTTPStep{Method: "GET", URL: "{{baseUrl}}/orders/{{orderId}}"}},
	}}
}

func TestOrderedStepsCapturesAndVariants(t *testing.T) {
	e, st := fixture(t)
	save(t, st, lifecycle())
	res, err := e.Run(context.Background(), Options{App: "shop"})
	must(t, err)
	if res.Status != model.StatusPassed || res.Summary.Total != 8 || res.Summary.Passed != 8 {
		t.Fatalf("summary %+v, statuses %v", res.Summary, statuses(res))
	}
	var order []string
	for _, tr := range res.Scenarios[0].Tests {
		order = append(order, tr.ID)
	}
	if got := strings.Join(order, " "); got != "create create.no-token create.stale-token create.zero create.also-ok read delete gone" {
		t.Fatalf("order of operations: %s", got)
	}
	create := find(t, res, "create")
	if create.Captured["orderId"] == "" || create.Captured["where"] != "/orders/"+create.Captured["orderId"] {
		t.Fatalf("captures %v", create.Captured)
	}
	zero := find(t, res, "create.zero")
	if zero.Kind != model.KindNegative || zero.VariantOf != "create" || zero.Properties["priority"] != "high" {
		t.Fatalf("variant metadata %+v", zero)
	}
	body := zero.Request.(map[string]any)["body"].(map[string]any)
	if body["item"] != "widget" || fmt.Sprint(body["quantity"]) != "0" {
		t.Fatalf("variant override did not merge over the parent body: %v", body)
	}
	if find(t, res, "create.also-ok").Properties["priority"] != "low" {
		t.Fatal("variant property should override the parent's")
	}
	// The variant created a second order; later steps must still use the id
	// captured by the parent, never one from a variant.
	if find(t, res, "read").Status != model.StatusPassed {
		t.Fatal("read should use the parent's captured id")
	}
}

func TestFailureHaltsLaterStepsUnlessContinueOnFail(t *testing.T) {
	e, st := fixture(t)
	sc := lifecycle()
	sc.Tests[0].Expect.Status = intp(200) // wrong on purpose: the API answers 201
	save(t, st, sc)
	res, err := e.Run(context.Background(), Options{App: "shop"})
	must(t, err)
	got := statuses(res)
	if got["create"] != model.StatusFailed || got["read"] != model.StatusSkipped || got["delete"] != model.StatusSkipped || got["gone"] != model.StatusSkipped {
		t.Fatalf("statuses %v", got)
	}
	if got["create.zero"] != model.StatusPassed {
		t.Fatalf("variants of a failed parent still run: %v", got)
	}
	if res.Status != model.StatusFailed || res.Summary.Failed != 1 || res.Summary.Skipped != 3 {
		t.Fatalf("summary %+v", res.Summary)
	}

	sc.Tests[0].ContinueOnFail = true
	save(t, st, sc)
	res, err = e.Run(context.Background(), Options{App: "shop"})
	must(t, err)
	// Nothing was captured, so the next step reports the undefined variable.
	read := find(t, res, "read")
	if read.Status != model.StatusError || !strings.Contains(read.Error, "{{where}}") {
		t.Fatalf("read: %+v", read)
	}
}

func TestDefaultExpectations(t *testing.T) {
	e, st := fixture(t)
	save(t, st, &model.Scenario{ID: "defaults", Auth: "none", Tests: []model.Test{
		{ID: "pos-ok", Type: model.TypeHTTP, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/health"}},
		{ID: "pos-bad", Type: model.TypeHTTP, ContinueOnFail: true, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/1"}},
		{ID: "neg-ok", Type: model.TypeHTTP, Kind: model.KindNegative, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/1"}},
		{ID: "neg-bad", Type: model.TypeHTTP, Kind: model.KindNegative, ContinueOnFail: true, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/health"}},
		{ID: "refused", Type: model.TypeHTTP, Kind: model.KindNegative, ContinueOnFail: true, HTTP: &model.HTTPStep{URL: "http://127.0.0.1:1/x"}},
		{ID: "refused-expected", Type: model.TypeHTTP, Kind: model.KindNegative, HTTP: &model.HTTPStep{URL: "http://127.0.0.1:1/x"}, Expect: &model.Expect{Error: "connection refused"}},
		{ID: "slow", Type: model.TypeHTTP, ContinueOnFail: true, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/health"}, Expect: &model.Expect{Headers: map[string]string{"content-type": "text/plain"}, BodyContains: []string{"up", "down"}}},
	}})
	res, err := e.Run(context.Background(), Options{App: "shop"})
	must(t, err)
	want := map[string]string{"pos-ok": "passed", "pos-bad": "failed", "neg-ok": "passed", "neg-bad": "failed", "refused": "error", "refused-expected": "passed", "slow": "failed"}
	got := statuses(res)
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %s, want %s", id, got[id], w)
		}
	}
	failed := 0
	for _, a := range find(t, res, "slow").Assertions {
		if !a.Passed {
			failed++
		}
	}
	if failed != 2 { // header mismatch and the "down" substring
		t.Errorf("slow: %d failed assertions, want 2: %+v", failed, find(t, res, "slow").Assertions)
	}
}

func TestAuthSelectionAndTokenFailure(t *testing.T) {
	e, st := fixture(t)
	save(t, st, &model.Scenario{ID: "auth", Tests: []model.Test{
		{ID: "default-auth", Type: model.TypeHTTP, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/1"}, Expect: &model.Expect{Status: intp(404)}},
		{ID: "bad-secret", Type: model.TypeHTTP, Auth: "bad-secret", ContinueOnFail: true, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/1"}},
		{ID: "bad-secret-expected", Type: model.TypeHTTP, Kind: model.KindNegative, Auth: "bad-secret", HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/1"}, Expect: &model.Expect{Error: "answered 401"}},
		{ID: "unknown-profile", Type: model.TypeHTTP, Auth: "ghost", ContinueOnFail: true, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/health"}},
		{ID: "header-override", Type: model.TypeHTTP, Kind: model.KindNegative, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/1", Headers: map[string]string{"Authorization": ""}}, Expect: &model.Expect{Status: intp(401)}},
	}})
	res, err := e.Run(context.Background(), Options{App: "shop"})
	must(t, err)
	want := map[string]string{"default-auth": "passed", "bad-secret": "error", "bad-secret-expected": "passed", "unknown-profile": "error", "header-override": "passed"}
	got := statuses(res)
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %s, want %s (%s)", id, got[id], w, find(t, res, id).Error)
		}
	}
}

func TestGRPCAndMTLS(t *testing.T) {
	e, st := fixture(t)
	save(t, st, &model.Scenario{ID: "platform", Auth: "none", Tests: []model.Test{
		{ID: "health", Type: model.TypeGRPC, GRPC: &model.GRPCStep{Target: "{{grpcTarget}}", Method: "grpc.health.v1.Health/Check", Plaintext: true, Message: map[string]any{"service": "orders"}},
			Expect:  &model.Expect{Code: "ok", Body: []model.Assertion{{Path: "status", Value: "SERVING"}}},
			Capture: map[string]string{"state": "status"},
			Variants: []model.Variant{
				{ID: "unknown", GRPC: map[string]any{"message": map[string]any{"service": "ghost"}}, Expect: &model.Expect{Code: "NOT_FOUND"}},
				{ID: "unknown-default", GRPC: map[string]any{"message": map[string]any{"service": "ghost"}}},
				{ID: "wrong-code", GRPC: map[string]any{"message": map[string]any{"service": "ghost"}}, Expect: &model.Expect{Code: "PERMISSION_DENIED"}},
			}},
		{ID: "no-such-method", Type: model.TypeGRPC, ContinueOnFail: true, GRPC: &model.GRPCStep{Target: "{{grpcTarget}}", Method: "grpc.health.v1.Health/Nope", Plaintext: true}},
		{ID: "mtls", Type: model.TypeHTTP, Auth: "mtls", HTTP: &model.HTTPStep{URL: "{{secureUrl}}/secure/ping"},
			Expect: &model.Expect{Body: []model.Assertion{{Path: "client", Value: "testplane-demo-client"}}},
			Variants: []model.Variant{
				{ID: "no-cert", Auth: "ca-only", Expect: &model.Expect{Error: "*"}},
				{ID: "untrusted-server", Auth: "none", Expect: &model.Expect{Error: "certificate"}},
			}},
	}})
	res, err := e.Run(context.Background(), Options{App: "shop"})
	must(t, err)
	want := map[string]string{"health": "passed", "health.unknown": "passed", "health.unknown-default": "passed", "health.wrong-code": "failed",
		"no-such-method": "error", "mtls": "passed", "mtls.no-cert": "passed", "mtls.untrusted-server": "passed"}
	got := statuses(res)
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %s, want %s (%+v)", id, got[id], w, find(t, res, id))
		}
	}
	if find(t, res, "health").Captured["state"] != "SERVING" {
		t.Error("capture from a gRPC response")
	}
}

func TestSelectionFiltersAndOverrides(t *testing.T) {
	e, st := fixture(t)
	a := lifecycle()
	a.Tags = []string{"smoke"}
	save(t, st, a)
	save(t, st, &model.Scenario{ID: "other", Auth: "none", Tags: []string{"slow"}, Tests: []model.Test{{ID: "h", Type: model.TypeHTTP, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/health"}}}})
	ctx := context.Background()

	res, err := e.Run(ctx, Options{App: "shop", Scenarios: []string{"lifecycle"}})
	must(t, err)
	if len(res.Scenarios) != 1 || res.Scenarios[0].ID != "lifecycle" {
		t.Fatalf("scenario filter ran %d scenarios", len(res.Scenarios))
	}
	res, err = e.Run(ctx, Options{App: "shop", Tags: []string{"SLOW"}})
	must(t, err)
	if len(res.Scenarios) != 1 || res.Scenarios[0].ID != "other" {
		t.Fatalf("tag filter: %+v", res.Scenarios)
	}
	if _, err := e.Run(ctx, Options{App: "shop", Scenarios: []string{"ghost"}}); err == nil {
		t.Fatal("unknown scenario must be an error")
	}
	if _, err := e.Run(ctx, Options{App: "shop", Env: "ghost"}); err == nil {
		t.Fatal("unknown environment must be an error")
	}
	if _, err := e.Run(ctx, Options{App: "ghost"}); err == nil {
		t.Fatal("unknown application must be an error")
	}

	res, err = e.Run(ctx, Options{App: "shop", Scenarios: []string{"lifecycle"}, Kind: model.KindPositive})
	must(t, err)
	got := statuses(res)
	if got["create"] != "passed" || got["create.zero"] != "skipped" || got["create.also-ok"] != "passed" || got["gone"] != "skipped" || res.Status != model.StatusPassed {
		t.Fatalf("kind=positive: %v", got)
	}
	res, err = e.Run(ctx, Options{App: "shop", Scenarios: []string{"lifecycle"}, Kind: model.KindNegative})
	must(t, err)
	got = statuses(res)
	if got["create"] != "passed" || got["create.zero"] != "passed" || got["create.also-ok"] != "skipped" || got["gone"] != "passed" {
		t.Fatalf("kind=negative: %v", got)
	}

	res, err = e.Run(ctx, Options{App: "shop", Scenarios: []string{"lifecycle"}, Vars: map[string]string{"item": "gadget"}})
	must(t, err)
	if b := find(t, res, "create").Request.(map[string]any)["body"].(map[string]any); b["item"] != "gadget" {
		t.Fatalf("--var should override a scenario variable: %v", b)
	}
	res, err = e.Run(ctx, Options{App: "shop", Scenarios: []string{"other"}, Env: "other"})
	must(t, err)
	if statuses(res)["h"] != model.StatusError {
		t.Fatal("the other environment points at a closed port")
	}
}

func TestSkipRetryAndBrokenScenarioFile(t *testing.T) {
	e, st := fixture(t)
	hits := 0
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(503)
		}
	}))
	defer flaky.Close()
	save(t, st, &model.Scenario{ID: "misc", Auth: "none", Tests: []model.Test{
		{ID: "skipped", Type: model.TypeHTTP, Skip: true, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/health"}, Variants: []model.Variant{{ID: "v"}}},
		{ID: "eventually", Type: model.TypeHTTP, Retry: &model.Retry{Attempts: 5, DelayMs: 1}, HTTP: &model.HTTPStep{URL: flaky.URL}},
	}})
	must(t, os.WriteFile(filepath.Join(st.AppDir("shop"), "scenarios", "broken.yaml"), []byte("id: broken\nname: Broken\ntests:\n  - id: x\n    type: http\n    htpp: {url: nope}\n"), 0o644))
	res, err := e.Run(context.Background(), Options{App: "shop"})
	must(t, err)
	got := statuses(res)
	if got["skipped"] != "skipped" || got["skipped.v"] != "skipped" {
		t.Errorf("skip: %v", got)
	}
	if ev := find(t, res, "eventually"); ev.Status != "passed" || ev.Attempts != 3 {
		t.Errorf("retry: %+v", ev)
	}
	if b := find(t, res, "broken"); b.Status != model.StatusError || !strings.Contains(b.Error, "htpp") {
		t.Errorf("a mistyped field must fail loudly, got %+v", b)
	}
	if res.Status != model.StatusFailed {
		t.Error("run with a broken scenario file must fail")
	}
}

func TestCheckAuth(t *testing.T) {
	e, _ := fixture(t)
	ctx := context.Background()
	ok, err := e.CheckAuth(ctx, "shop", "oauth", "", "")
	must(t, err)
	if !ok.OK || len(ok.Sends) != 1 || ok.Sends[0] != "header Authorization" {
		t.Fatalf("oauth: %+v", ok)
	}
	bad, err := e.CheckAuth(ctx, "shop", "bad-secret", "", "")
	must(t, err)
	if bad.OK || !strings.Contains(bad.Error, "401") {
		t.Fatalf("bad secret: %+v", bad)
	}
	m, err := e.CheckAuth(ctx, "shop", "mtls", "", "")
	must(t, err)
	if !m.OK || !m.ClientCert || len(m.CertChecks) != 3 || m.CertChecks[0].Status != "ok" {
		t.Fatalf("mtls: %+v", m)
	}
}

// Runs against a real server when TESTPLANE_TEST_PG_DSN is set.
func TestPostgresStepsLive(t *testing.T) {
	dsn := os.Getenv("TESTPLANE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TESTPLANE_TEST_PG_DSN not set")
	}
	e, st := fixture(t)
	pg := func(q string, args ...any) *model.PostgresStep {
		return &model.PostgresStep{DSN: "{{dsn}}", Query: q, Args: args}
	}
	save(t, st, &model.Scenario{ID: "db", Auth: "none", Variables: map[string]string{"dsn": dsn, "ref": "ref-{{$randomInt}}"}, Tests: []model.Test{
		{ID: "setup", Type: model.TypePostgres, Postgres: pg("CREATE TABLE IF NOT EXISTS tp_engine (ref text primary key, status text)")},
		{ID: "insert", Type: model.TypePostgres, Postgres: pg("INSERT INTO tp_engine VALUES ($1, 'created')", "{{ref}}")},
		{ID: "exists", Type: model.TypePostgres, Postgres: pg("SELECT ref, status FROM tp_engine WHERE ref = $1", "{{ref}}"),
			Expect:  &model.Expect{Count: intp(1), Body: []model.Assertion{{Path: "rows.0.status", Value: "created"}}},
			Capture: map[string]string{"status": "rows.0.status"},
			Variants: []model.Variant{
				{ID: "other-ref", Postgres: map[string]any{"args": []any{"no-such-ref"}}},
				{ID: "wrong-default", Kind: model.KindPositive, Postgres: map[string]any{"args": []any{"no-such-ref"}}},
			}},
		{ID: "uses-capture", Type: model.TypePostgres, Postgres: pg("SELECT 1 AS one WHERE $1 = 'created'", "{{status}}")},
		{ID: "bad-sql", Type: model.TypePostgres, ContinueOnFail: true, Postgres: pg("SELECT * FROM tp_no_such_table")},
		{ID: "cleanup", Type: model.TypePostgres, Postgres: pg("DELETE FROM tp_engine WHERE ref = $1", "{{ref}}")},
		{ID: "gone", Type: model.TypePostgres, Kind: model.KindNegative, Postgres: pg("SELECT ref FROM tp_engine WHERE ref = $1", "{{ref}}")},
	}})
	res, err := e.Run(context.Background(), Options{App: "shop"})
	must(t, err)
	want := map[string]string{"setup": "passed", "insert": "passed", "exists": "passed", "exists.other-ref": "passed", "exists.wrong-default": "failed",
		"uses-capture": "passed", "bad-sql": "error", "cleanup": "passed", "gone": "passed"}
	got := statuses(res)
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %s, want %s (%+v)", id, got[id], w, find(t, res, id))
		}
	}
}
