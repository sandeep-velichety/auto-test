package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/testplane/testplane/internal/model"
)

// A "team" application runs a flow across services, borrowing each
// service's auth profile and environment values.
func TestFlowAcrossApplications(t *testing.T) {
	e, st := fixture(t) // application "shop" with profiles oauth, mtls, stale
	shop, _ := st.GetApp("shop")
	must(t, st.SaveApp(&model.Application{ID: "team", Name: "Team flows", DefaultEnvironment: "test",
		Environments: map[string]map[string]string{"test": {"baseUrl": "http://127.0.0.1:1"}}}))
	sc := &model.Scenario{ID: "flow", Name: "Flow", Tests: []model.Test{
		{ID: "create", Name: "Create in shop", Type: model.TypeHTTP, Auth: "shop/oauth",
			HTTP:    &model.HTTPStep{Method: "POST", URL: "{{shop.baseUrl}}/orders", Body: map[string]any{"item": "x", "quantity": 1}},
			Capture: map[string]string{"id": "id"},
			Variants: []model.Variant{
				{ID: "stale", Name: "Stale", Auth: "shop/stale"},
				{ID: "anon", Name: "Anonymous", Auth: "none"},
			}},
		{ID: "secure", Name: "mTLS with shop's certificate", Type: model.TypeHTTP, Auth: "shop/mtls", HTTP: &model.HTTPStep{URL: "{{shop.secureUrl}}/secure/ping"}},
		{ID: "ghost-app", Name: "Unknown app", Type: model.TypeHTTP, Auth: "ghost/oauth", ContinueOnFail: true, HTTP: &model.HTTPStep{URL: "{{shop.baseUrl}}/health"}},
		{ID: "ghost-var", Name: "Unknown var", Type: model.TypeHTTP, Auth: "none", ContinueOnFail: true, HTTP: &model.HTTPStep{URL: "{{shop.nope}}/health"}},
	}}
	must(t, st.SaveScenario("team", sc))
	res, err := e.Run(context.Background(), Options{App: "team"})
	must(t, err)
	want := map[string]string{"create": "passed", "create.stale": "passed", "create.anon": "passed", "secure": "passed", "ghost-app": "error", "ghost-var": "error"}
	got := statuses(res)
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %s, want %s (%s)", id, got[id], w, find(t, res, id).Error)
		}
	}
	// The borrowed profile's {{baseUrl}} must be shop's, not team's (which
	// points at a closed port): the token request above proves it. The
	// reverse also holds: team's own values are untouched.
	if u := find(t, res, "create").Request.(map[string]any)["url"]; u != shop.Environments["test"]["baseUrl"]+"/orders" {
		t.Errorf("url %v", u)
	}
	if !strings.Contains(find(t, res, "ghost-var").Error, "{{shop.nope}}") {
		t.Errorf("unknown cross-app variable: %s", find(t, res, "ghost-var").Error)
	}
}

func TestLogsStep(t *testing.T) {
	var gotBody map[string]any
	var gotKeys [2]string
	dd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v2/logs/events/search" {
			http.NotFound(w, r)
			return
		}
		gotKeys = [2]string{r.Header.Get("DD-API-KEY"), r.Header.Get("DD-APPLICATION-KEY")}
		if gotKeys[0] != "api-123" {
			http.Error(w, `{"errors":["Forbidden"]}`, 403)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		gotBody = map[string]any{}
		_ = json.Unmarshal(raw, &gotBody)
		q := gotBody["filter"].(map[string]any)["query"].(string)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(q, "status:error") {
			_, _ = w.Write([]byte(`{"data":[],"meta":{"status":"done"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[
		 {"id":"a1","type":"log","attributes":{"timestamp":"2026-11-17T04:00:00.000Z","status":"info","service":"orders","host":"h1","message":"order 1001 created","tags":["env:test"],"attributes":{"orderId":"1001","http":{"status_code":201}}}},
		 {"id":"a2","type":"log","attributes":{"timestamp":"2026-11-17T04:00:01.000Z","status":"info","service":"orders","host":"h1","message":"event published","attributes":{"orderId":"1001"}}}
		]}`))
	}))
	defer dd.Close()
	t.Setenv("TP_DD_API", "api-123")
	t.Setenv("TP_DD_APP", "app-456")

	e, st := fixture(t)
	logs := func(q string) *model.LogsStep {
		return &model.LogsStep{URL: dd.URL, Query: q, APIKey: "{{$env.TP_DD_API}}", AppKey: "{{$env.TP_DD_APP}}", Limit: 25}
	}
	before := time.Now().Add(-10 * time.Second)
	save(t, st, &model.Scenario{ID: "logs", Auth: "none", Variables: map[string]string{"orderId": "1001"}, Tests: []model.Test{
		{ID: "appeared", Type: model.TypeLogs, Logs: logs("service:orders @orderId:{{orderId}}"),
			Expect:  &model.Expect{Count: intp(2), Body: []model.Assertion{{Path: "logs.0.message", Op: "contains", Value: "created"}, {Path: "logs.0.attributes.http.status_code", Value: 201}}},
			Capture: map[string]string{"firstLog": "logs.0.id"},
			Variants: []model.Variant{
				{ID: "no-errors", Name: "No error logs", Logs: map[string]any{"query": "service:orders status:error"}},
				{ID: "errors-wanted", Name: "Expecting errors fails", Kind: model.KindPositive, Logs: map[string]any{"query": "service:orders status:error"}},
				{ID: "bad-key", Name: "Rejected key", Logs: map[string]any{"apiKey": "wrong"}, Expect: &model.Expect{Error: "answered 403"}},
			}},
		{ID: "default-positive", Type: model.TypeLogs, Logs: logs("service:orders")},
		{ID: "no-keys", Type: model.TypeLogs, ContinueOnFail: true, Logs: &model.LogsStep{URL: dd.URL, Query: "*"}},
		{ID: "other-provider", Type: model.TypeLogs, ContinueOnFail: true, Logs: &model.LogsStep{Provider: "splunk", Query: "*"}},
	}})
	res, err := e.Run(context.Background(), Options{App: "shop"})
	must(t, err)
	want := map[string]string{"appeared": "passed", "appeared.no-errors": "passed", "appeared.errors-wanted": "failed", "appeared.bad-key": "passed",
		"default-positive": "passed", "no-keys": "error", "other-provider": "error"}
	got := statuses(res)
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %s, want %s (%+v)", id, got[id], w, find(t, res, id))
		}
	}
	first := find(t, res, "appeared")
	if first.Captured["firstLog"] != "a1" {
		t.Errorf("capture: %v", first.Captured)
	}
	// Keys reach Datadog but are never stored with the run.
	if gotKeys != [2]string{"api-123", "app-456"} {
		t.Errorf("keys sent: %v", gotKeys)
	}
	req := first.Request.(map[string]any)
	stored, _ := json.Marshal(res)
	if req["apiKey"] != model.SecretMask || req["appKey"] != model.SecretMask || strings.Contains(string(stored), "api-123") || strings.Contains(string(stored), "app-456") {
		t.Errorf("keys must be masked in the stored run: %v", req)
	}
	// The search window starts when the scenario started.
	f := gotBody["filter"].(map[string]any)
	from, err := time.Parse(time.RFC3339, f["from"].(string))
	if err != nil || from.Before(before) || from.After(time.Now()) || f["to"] != "now" {
		t.Errorf("window: %v", f)
	}
	if gotBody["page"].(map[string]any)["limit"] != float64(25) || gotBody["sort"] != "timestamp" {
		t.Errorf("request body: %v", gotBody)
	}
	if !strings.Contains(find(t, res, "no-keys").Error, "API key") {
		t.Errorf("missing keys message: %s", find(t, res, "no-keys").Error)
	}
}
