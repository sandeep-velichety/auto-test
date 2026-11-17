package store

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testplane/testplane/internal/demo"
	"github.com/testplane/testplane/internal/model"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s := New(t.TempDir())
	if err := s.SaveApp(&model.Application{ID: "shop", Name: "Shop"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func scenario() *model.Scenario {
	status := 201
	return &model.Scenario{ID: "orders", Name: "Orders", Tags: []string{"smoke"}, Tests: []model.Test{{
		ID: "create", Name: "Create", Type: model.TypeHTTP, Properties: map[string]string{"priority": "high"},
		HTTP:     &model.HTTPStep{Method: "POST", URL: "{{baseUrl}}/orders", Headers: map[string]string{"X-A": "1"}, Body: map[string]any{"item": "x", "quantity": 2}},
		Expect:   &model.Expect{Status: &status, Body: []model.Assertion{{Path: "id", Op: "exists"}}},
		Capture:  map[string]string{"orderId": "id"},
		Variants: []model.Variant{{ID: "bad", Name: "Bad", HTTP: map[string]any{"body": map[string]any{"quantity": 0}}}},
	}}}
}

func TestScenarioRoundTripsThroughYAML(t *testing.T) {
	s := newStore(t)
	if err := s.SaveScenario("shop", scenario()); err != nil {
		t.Fatal(err)
	}
	raw, err := s.GetScenarioYAML("shop", "orders")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"id: orders", "type: http", "url: '{{baseUrl}}/orders'", "priority: high", "variants:"} {
		if !strings.Contains(strings.ReplaceAll(string(raw), `"`, `'`), want) {
			t.Errorf("YAML lacks %q:\n%s", want, raw)
		}
	}
	got, err := s.GetScenario("shop", "orders")
	if err != nil {
		t.Fatal(err)
	}
	tst := got.Tests[0]
	if *tst.Expect.Status != 201 || tst.HTTP.Headers["X-A"] != "1" || tst.Capture["orderId"] != "id" || len(tst.Variants) != 1 {
		t.Fatalf("round trip lost data: %+v", tst)
	}
	body := tst.Variants[0].HTTP["body"].(map[string]any)
	if len(body) != 1 {
		t.Fatalf("variant override: %v", body)
	}
	list, err := s.ListScenarios("shop")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
}

func TestRawYAMLIsValidatedAndKeptVerbatim(t *testing.T) {
	s := newStore(t)
	good := "# hand written\nid: orders\nname: Orders\ntests:\n  - id: ping\n    name: Ping\n    type: http\n    http:\n      url: http://x/health\n"
	if _, err := s.SaveScenarioYAML("shop", "orders", []byte(good)); err != nil {
		t.Fatal(err)
	}
	raw, _ := s.GetScenarioYAML("shop", "orders")
	if string(raw) != good {
		t.Fatal("comments and layout must survive")
	}
	bad := map[string]string{
		"unknown field": strings.Replace(good, "http:\n", "htpp:\n", 1),
		"wrong type":    strings.Replace(good, "type: http", "type: soap", 1),
		"id mismatch":   strings.Replace(good, "id: orders", "id: other", 1),
		"duplicate ids": good + "  - id: ping\n    name: Again\n    type: http\n    http:\n      url: http://x\n",
		"missing url":   strings.Replace(good, "      url: http://x/health\n", "      method: GET\n", 1),
	}
	for name, y := range bad {
		if _, err := s.SaveScenarioYAML("shop", "orders", []byte(y)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	raw, _ = s.GetScenarioYAML("shop", "orders")
	if string(raw) != good {
		t.Fatal("a rejected save must not change the file")
	}
}

func TestIDsCannotEscapeTheWorkspace(t *testing.T) {
	s := newStore(t)
	for _, id := range []string{"../x", "a/b", "", "UPPER", ".hidden", "a b"} {
		if err := s.SaveApp(&model.Application{ID: id}); !errors.Is(err, ErrInvalid) {
			t.Errorf("app id %q accepted: %v", id, err)
		}
		if _, err := s.GetScenario("shop", id); !errors.Is(err, ErrInvalid) {
			t.Errorf("scenario id %q accepted: %v", id, err)
		}
		if _, err := s.GetAuth("shop", id); !errors.Is(err, ErrInvalid) {
			t.Errorf("auth id %q accepted: %v", id, err)
		}
	}
	for _, name := range []string{"../../etc/passwd", "a/b.pem", "", ".."} {
		if err := s.SaveAsset("shop", "certs", name, strings.NewReader("x")); !errors.Is(err, ErrInvalid) {
			t.Errorf("asset name %q accepted: %v", name, err)
		}
	}
	if err := s.SaveAsset("shop", "auth", "x.pem", strings.NewReader("x")); !errors.Is(err, ErrInvalid) {
		t.Error("auth is not an uploadable asset category")
	}
	if _, err := s.GetScenario("shop", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := s.SaveScenario("ghost", scenario()); !errors.Is(err, ErrNotFound) {
		t.Errorf("saving into a missing app: %v", err)
	}
}

func TestAssetsDescribeCertificatesWithoutLeakingKeys(t *testing.T) {
	s := newStore(t)
	certs, err := demo.NewCerts()
	if err != nil {
		t.Fatal(err)
	}
	if err := certs.WriteClientFiles(s.AssetDir("shop", "certs")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAsset("shop", "protos", "orders.proto", strings.NewReader(`syntax = "proto3";`)); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListAssets("shop")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]Asset{}
	for _, a := range list {
		kinds[a.Name] = a
	}
	if kinds["demo-client.crt"].Kind != "certificate" || !strings.Contains(kinds["demo-client.crt"].Subject, "testplane-demo-client") || kinds["demo-client.crt"].Expired {
		t.Errorf("client cert: %+v", kinds["demo-client.crt"])
	}
	if kinds["demo-ca.crt"].Kind != "certificate" || !kinds["demo-ca.crt"].IsCA {
		t.Errorf("ca: %+v", kinds["demo-ca.crt"])
	}
	if kinds["demo-client.key"].Kind != "private-key" || kinds["demo-client.key"].Subject != "" {
		t.Errorf("key: %+v", kinds["demo-client.key"])
	}
	if kinds["orders.proto"].Kind != "proto" {
		t.Errorf("proto: %+v", kinds["orders.proto"])
	}
	info, _ := os.Stat(filepath.Join(s.AssetDir("shop", "certs"), "demo-client.key"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %v", info.Mode().Perm())
	}
	if err := s.DeleteAsset("shop", "certs", "demo-ca.crt"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAsset("shop", "certs", "demo-ca.crt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestExportLeavesPrivateKeysOutAndImportRestores(t *testing.T) {
	s := newStore(t)
	certs, _ := demo.NewCerts()
	_ = certs.WriteClientFiles(s.AssetDir("shop", "certs"))
	_ = s.SaveScenario("shop", scenario())
	_ = s.SaveAuth("shop", &model.AuthProfile{ID: "token", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "{{$env.TOKEN}}"}})

	names := func(includeKeys bool) (map[string]bool, []byte) {
		var buf bytes.Buffer
		if err := s.ExportApp("shop", &buf, includeKeys); err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, f := range zr.File {
			out[f.Name] = true
		}
		return out, buf.Bytes()
	}
	without, data := names(false)
	if without["shop/assets/certs/demo-client.key"] || !without["shop/assets/certs/demo-client.crt"] || !without["shop/scenarios/orders.yaml"] || !without["shop/assets/auth/token.yaml"] || !without["shop/application.yaml"] {
		t.Fatalf("export without keys: %v", without)
	}
	with, _ := names(true)
	if !with["shop/assets/certs/demo-client.key"] {
		t.Fatal("export with keys should include the key")
	}

	other := New(t.TempDir())
	id, err := other.ImportApp(data)
	if err != nil || id != "shop" {
		t.Fatalf("import: %q %v", id, err)
	}
	if _, err := other.GetScenario("shop", "orders"); err != nil {
		t.Fatal(err)
	}
	if p, err := other.GetAuth("shop", "token"); err != nil || p.Bearer.Token != "{{$env.TOKEN}}" {
		t.Fatalf("auth after import: %+v %v", p, err)
	}

	var evil bytes.Buffer
	zw := zip.NewWriter(&evil)
	f, _ := zw.Create("shop/../../outside.txt")
	_, _ = f.Write([]byte("x"))
	_ = zw.Close()
	if _, err := other.ImportApp(evil.Bytes()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zip with a path escape must be refused, got %v", err)
	}
	if _, err := other.ImportApp([]byte("not a zip")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("garbage must be refused, got %v", err)
	}
}

func TestRunHistoryFiltersAndRetention(t *testing.T) {
	s := newStore(t)
	day := func(d int) time.Time { return time.Date(2026, 1, d, 12, 0, 0, 0, time.UTC) }
	mk := func(id string, d int, env string, scenarios ...model.ScenarioResult) {
		r := &model.RunResult{ID: id, Application: "shop", Environment: env, StartedAt: day(d), Status: model.StatusPassed, Scenarios: scenarios}
		for _, sc := range scenarios {
			if sc.Status == model.StatusFailed {
				r.Status = model.StatusFailed
			}
		}
		if err := s.SaveRun(r); err != nil {
			t.Fatal(err)
		}
	}
	pass := func(id string) model.ScenarioResult {
		return model.ScenarioResult{ID: id, Status: model.StatusPassed, DurationMs: 10, Tests: []model.TestResult{{ID: "t", Status: model.StatusPassed}}}
	}
	failed := func(id string) model.ScenarioResult {
		return model.ScenarioResult{ID: id, Status: model.StatusFailed, DurationMs: 20, Tests: []model.TestResult{{ID: "t", Status: model.StatusFailed}, {ID: "u", Status: model.StatusSkipped}}}
	}
	mk("20260101-120000-aa", 1, "staging", pass("orders"))
	mk("20260103-120000-cc", 3, "staging", pass("orders"), failed("billing"))
	mk("20260102-120000-bb", 2, "prod", failed("orders"))

	ids := func(f RunFilter) string {
		list, err := s.ListRuns("shop", f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range list {
			out = append(out, r.ID[6:8])
			if len(r.Scenarios) > 0 && r.Scenarios[0].Tests != nil {
				t.Error("list should omit test detail")
			}
		}
		return strings.Join(out, " ")
	}
	cases := []struct {
		name string
		f    RunFilter
		want string
	}{
		{"all, newest first", RunFilter{}, "03 02 01"},
		{"limit", RunFilter{Limit: 2}, "03 02"},
		{"environment", RunFilter{Environment: "staging"}, "03 01"},
		{"run status", RunFilter{Status: model.StatusFailed}, "03 02"},
		{"from", RunFilter{From: day(2)}, "03 02"},
		{"to", RunFilter{To: day(2)}, "02 01"},
		{"window", RunFilter{From: day(2), To: day(2)}, "02"},
		{"scenario", RunFilter{Scenario: "orders"}, "03 02 01"},
		{"scenario that ran once", RunFilter{Scenario: "billing"}, "03"},
		{"scenario status uses the scenario, not the run", RunFilter{Scenario: "orders", Status: model.StatusPassed}, "03 01"},
		{"unknown scenario", RunFilter{Scenario: "ghost"}, ""},
	}
	for _, c := range cases {
		if got := ids(c.f); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	list, _ := s.ListRuns("shop", RunFilter{Scenario: "billing"})
	if r := list[0]; r.Status != model.StatusFailed || r.DurationMs != 20 || r.Summary != (model.Summary{Total: 2, Failed: 1, Skipped: 1}) || len(r.Scenarios) != 1 {
		t.Errorf("scenario view of a run: %+v", r)
	}
	full, err := s.GetRun("shop", "20260101-120000-aa")
	if err != nil || len(full.Scenarios[0].Tests) != 1 {
		t.Fatalf("get: %+v %v", full, err)
	}
	if err := s.SaveRun(&model.RunResult{ID: "../../evil", Application: "shop"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("run id with a path must be refused: %v", err)
	}

	n, err := s.PruneRuns(day(3))
	if err != nil || n != 2 {
		t.Fatalf("prune removed %d (%v), want 2", n, err)
	}
	if got := ids(RunFilter{}); got != "03" {
		t.Errorf("after prune: %q", got)
	}
}

func TestExportReplacesLiteralSecrets(t *testing.T) {
	s := newStore(t)
	_ = s.SaveAuth("shop", &model.AuthProfile{ID: "literal", Type: model.AuthOAuth2ClientCredentials, OAuth2: &model.OAuth2Auth{TokenURL: "http://x/token", ClientID: "id", ClientSecret: "hunter2"}})
	_ = s.SaveAuth("shop", &model.AuthProfile{ID: "byenv", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "{{$env.TOKEN}}"}})
	var buf bytes.Buffer
	if err := s.ExportApp("shop", &buf, false); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("hunter2")) {
		t.Fatal("zip is compressed; check the entries instead")
	}
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	content := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		content[f.Name] = string(b)
	}
	lit := content["shop/assets/auth/literal.yaml"]
	if strings.Contains(lit, "hunter2") || !strings.Contains(lit, "CHANGE_ME") || !strings.Contains(lit, "clientId: id") {
		t.Errorf("literal secret must be replaced:\n%s", lit)
	}
	if !strings.Contains(content["shop/assets/auth/byenv.yaml"], "{{$env.TOKEN}}") {
		t.Errorf("an env reference is not a secret and must survive:\n%s", content["shop/assets/auth/byenv.yaml"])
	}
	buf.Reset()
	_ = s.ExportApp("shop", &buf, true)
	zr, _ = zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	for _, f := range zr.File {
		if f.Name == "shop/assets/auth/literal.yaml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			if !strings.Contains(string(b), "hunter2") {
				t.Error("a full export keeps secrets")
			}
		}
	}
}
