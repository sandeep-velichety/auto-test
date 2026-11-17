package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/testplane/testplane/internal/demo"
	"github.com/testplane/testplane/internal/model"
)

func bundleFixture(t *testing.T) *Store {
	t.Helper()
	s := New(t.TempDir())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SaveApp(&model.Application{ID: "shop", Name: "Shop", DefaultEnvironment: "dev", DefaultAuth: "fallback",
		Environments: map[string]map[string]string{"dev": {"baseUrl": "http://x", "secureHost": "x:8443"}}}))
	certs, _ := demo.NewCerts()
	must(certs.WriteClientFiles(s.AssetDir("shop", "certs")))
	must(s.SaveAuth("shop", &model.AuthProfile{ID: "token", Type: model.AuthOAuth2ClientCredentials, OAuth2: &model.OAuth2Auth{TokenURL: "{{idpUrl}}/token", ClientID: "c", ClientSecret: "hunter2"}}))
	must(s.SaveAuth("shop", &model.AuthProfile{ID: "fallback", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "{{$env.TOKEN}}"}}))
	must(s.SaveAuth("shop", &model.AuthProfile{ID: "unused", Type: model.AuthBasic, Basic: &model.BasicAuth{Username: "u", Password: "pw"}}))
	must(s.SaveCertSet("shop", &model.CertSet{ID: "client", Cert: "demo-client.crt", Key: "demo-client.key", CA: "demo-ca.crt", Environments: map[string]model.TLSSettings{"stg": {Cert: "stg.crt", Key: "stg.key"}}}))
	must(s.SaveCertSet("shop", &model.CertSet{ID: "by-host", Hosts: []string{"{{secureHost}}"}, CA: "demo-ca.crt"}))
	must(s.SaveCertSet("shop", &model.CertSet{ID: "unused-set", CA: "demo-ca.crt"}))
	must(s.SaveScenario("shop", &model.Scenario{ID: "flow", Name: "Flow", Variables: map[string]string{"item": "x-{{$uuid}}"}, Tests: []model.Test{
		{ID: "create", Name: "Create", Type: model.TypeHTTP, Auth: "token", MTLS: "client", HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/{{item}}"}, Capture: map[string]string{"orderId": "id"},
			Variants: []model.Variant{{ID: "other", Name: "Other", Auth: "team/shared", MTLS: "none"}}},
		{ID: "read", Name: "Read", Type: model.TypeHTTP, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/{{orderId}}?t={{tenant}}"}},
		{ID: "rpc", Name: "RPC", Type: model.TypeGRPC, Auth: "none", GRPC: &model.GRPCStep{Target: "{{grpcTarget}}", Method: "a.B/C", ProtoFiles: []string{"orders.proto"}}},
	}}))
	return s
}

func TestExportScenarioIsTheWholeScenarioInOneFile(t *testing.T) {
	s := bundleFixture(t)
	raw, err := s.ExportScenario("shop", "flow")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"kind: testplane/scenario", "application: shop", "id: flow", "id: token", "id: fallback", "id: client", "id: by-host", "CHANGE_ME", "{{$env.TOKEN}}", "stg.crt"} {
		if !strings.Contains(text, want) {
			t.Errorf("export lacks %q", want)
		}
	}
	for _, not := range []string{"hunter2", "id: unused", "unused-set", "PRIVATE KEY", "********"} {
		if strings.Contains(text, not) {
			t.Errorf("export must not contain %q", not)
		}
	}
	var b Bundle
	if err := yamlStrict(raw, &b); err != nil {
		t.Fatalf("export is not valid YAML for a bundle: %v", err)
	}
	if b.Scenario.Auth != "fallback" {
		t.Errorf("the application's default auth, which a test relies on, should be stated on the exported scenario: %q", b.Scenario.Auth)
	}
	if sc, _ := s.GetScenario("shop", "flow"); sc.Auth != "" {
		t.Error("exporting must not change the stored scenario")
	}
	r := b.Requires
	if got := strings.Join(r.Variables, ","); got != "baseUrl,grpcTarget,idpUrl,secureHost,tenant" {
		t.Errorf("variables the receiver must define (not the scenario's own, captured or built-in ones): %s", got)
	}
	if got := strings.Join(r.Files, ","); got != "certs/demo-ca.crt,certs/demo-client.crt,certs/demo-client.key,certs/stg.crt,certs/stg.key,protos/orders.proto" {
		t.Errorf("files: %s", got)
	}
	if got := strings.Join(r.External, ","); got != "team/shared" {
		t.Errorf("external: %s", got)
	}
	if got := strings.Join(r.Secrets, ","); got != "token" {
		t.Errorf("profiles with replaced secrets: %s", got)
	}
	if _, err := s.ExportScenario("shop", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown scenario: %v", err)
	}
}

func TestImportScenarioBundleAndPlainYAML(t *testing.T) {
	src := bundleFixture(t)
	raw, _ := src.ExportScenario("shop", "flow")

	dst := New(t.TempDir())
	_ = dst.SaveApp(&model.Application{ID: "orders", Name: "Orders", DefaultEnvironment: "dev", Environments: map[string]map[string]string{"dev": {"baseUrl": "http://y"}}})
	// The receiver already has its own "token" profile: it must be kept.
	_ = dst.SaveAuth("orders", &model.AuthProfile{ID: "token", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "mine"}})

	res, err := dst.ImportScenario("orders", raw, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scenario != "flow" || strings.Join(res.Kept, ",") != "auth profile token" {
		t.Fatalf("result: %+v", res)
	}
	if got := strings.Join(res.Created, ","); got != "auth profile fallback,certificate set by-host,certificate set client" {
		t.Errorf("created: %s", got)
	}
	missing := strings.Join(res.Missing, ",")
	for _, want := range []string{"variable grpcTarget", "variable tenant", "file certs/demo-client.key", "file protos/orders.proto"} {
		if !strings.Contains(missing, want) {
			t.Errorf("missing should list %q: %s", want, missing)
		}
	}
	if strings.Contains(missing, "variable baseUrl") {
		t.Errorf("baseUrl is defined by the receiver: %s", missing)
	}
	if p, _ := dst.GetAuth("orders", "token"); p.Bearer == nil || p.Bearer.Token != "mine" {
		t.Fatal("an existing profile must never be overwritten by an import")
	}
	if sc, err := dst.GetScenario("orders", "flow"); err != nil || len(sc.Tests) != 3 || sc.Tests[0].Variants[0].Auth != "team/shared" {
		t.Fatalf("scenario after import: %+v %v", sc, err)
	}
	if c, err := dst.GetCertSet("orders", "client"); err != nil || c.Environments["stg"].Cert != "stg.crt" {
		t.Fatalf("certificate set after import: %+v %v", c, err)
	}

	// Importing again needs overwrite; plain scenario YAML works too.
	if _, err := dst.ImportScenario("orders", raw, false); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second import without overwrite: %v", err)
	}
	if _, err := dst.ImportScenario("orders", raw, true); err != nil {
		t.Errorf("overwrite: %v", err)
	}
	plain, _ := src.GetScenarioYAML("shop", "flow")
	fresh := New(t.TempDir())
	_ = fresh.SaveApp(&model.Application{ID: "a", Name: "A"})
	if res, err := fresh.ImportScenario("a", plain, false); err != nil || res.Scenario != "flow" || len(res.Created) != 0 {
		t.Errorf("plain scenario YAML: %+v %v", res, err)
	}
	for name, bad := range map[string]string{
		"garbage":           "tests: [",
		"unknown field":     "kind: testplane/scenario\nscenario: {id: x, name: X, tests: []}\nextras: 1\n",
		"invalid scenario":  "kind: testplane/scenario\nscenario: {id: x, name: X, tests: [{id: t, name: T, type: soap}]}\n",
		"invalid profile":   "kind: testplane/scenario\nscenario: {id: x, name: X, tests: []}\nauth: [{id: p, name: P, type: basic}]\n",
		"path in cert file": "kind: testplane/scenario\nscenario: {id: x, name: X, tests: []}\ncertSets: [{id: c, name: C, cert: ../k.crt, key: k.key}]\n",
		"path in id":        "id: ../x\nname: X\ntests: []\n",
	} {
		if _, err := fresh.ImportScenario("a", []byte(bad), false); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if list, _ := fresh.ListAuth("a"); len(list) != 0 {
		t.Errorf("a rejected import must create nothing: %+v", list)
	}
	if _, err := fresh.ImportScenario("ghost", plain, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown application: %v", err)
	}
}

func yamlStrict(b []byte, v any) error { return yaml.UnmarshalWithOptions(b, v, yaml.Strict()) }
