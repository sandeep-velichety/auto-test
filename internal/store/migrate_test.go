package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/model"
)

func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

// legacyWorkspace is a workspace as it looked when auth and certificates
// lived inside each application: two applications whose ids and file names
// collide, and a scenario that borrows the other application's profile.
func legacyWorkspace(t *testing.T) string {
	root := t.TempDir()
	// orders
	put(t, root, "applications/orders/application.yaml", "id: orders\nname: Orders\ndefaultEnvironment: stage/NA\ndefaultAuth: token # the usual one\nenvironments:\n  stage/NA: {baseUrl: http://orders-na}\n  stage/AU: {baseUrl: http://orders-au}\n")
	put(t, root, "applications/orders/assets/auth/token.yaml", "# how orders gets its token\nid: token\nname: Orders token\ntype: oauth2_client_credentials\noauth2:\n  tokenUrl: '{{baseUrl}}/oauth/token' # issued by orders itself\n  clientId: qa\n  clientSecret: '{{$env.ORDERS_SECRET}}'\n")
	put(t, root, "applications/orders/assets/auth/plain.yaml", "id: plain\nname: Plain\ntype: bearer\nbearer: {token: abc}\n")
	put(t, root, "applications/orders/assets/tls/client.yaml", "id: client\nname: Client\ncert: a.crt\nkey: a.key\nhosts: ['{{baseUrl}}']\n")
	put(t, root, "applications/orders/assets/certs/a.crt", "ORDERS CERT")
	put(t, root, "applications/orders/assets/certs/a.key", "ORDERS KEY")
	put(t, root, "applications/orders/assets/certs/.gitkeep", "")
	put(t, root, "applications/orders/scenarios/flow.yaml", "id: flow\nname: Flow\nmtls: client\ntests:\n  - id: a\n    name: A\n    type: http\n    auth: token # default profile\n    http: {url: '{{baseUrl}}/x'}\n    variants:\n      - id: anon\n        name: Anonymous\n        auth: none\n")
	// billing: same ids and file names, different content
	put(t, root, "applications/billing/application.yaml", "id: billing\nname: Billing\ndefaultEnvironment: stage/NA\ndefaultAuth: token\nenvironments:\n  stage/NA: {baseUrl: http://billing-na}\n")
	put(t, root, "applications/billing/assets/auth/token.yaml", "id: token\nname: Billing token\ntype: bearer\nbearer: {token: billing-secret}\ntls: {cert: a.crt, key: a.key}\n")
	put(t, root, "applications/billing/assets/auth/plain.yaml", "id: plain\nname: Plain\ntype: bearer\nbearer:\n  token: abc\n") // same data, other layout
	put(t, root, "applications/billing/assets/tls/client.yaml", "id: client\nname: Billing client\ncert: a.crt\nkey: a.key\nenvironments:\n  stage: {ca: a.crt}\n")
	put(t, root, "applications/billing/assets/certs/a.crt", "BILLING CERT")
	put(t, root, "applications/billing/assets/certs/a.key", "ORDERS KEY") // identical file
	put(t, root, "applications/billing/scenarios/pay.yaml", "id: pay\nname: Pay\ntests:\n  - id: order\n    name: Order first\n    type: http\n    auth: orders/token   # borrowed from orders\n    mtls: \"orders/client\"\n    http: {url: '{{orders.baseUrl}}/x'}\n  - id: charge\n    name: Charge\n    type: http\n    auth: token\n    mtls: client\n    http: {url: '{{baseUrl}}/charge'}\n    variants:\n      - {id: plain, name: Plain, auth: plain}\n")
	return root
}

func TestMigrateMovesApplicationAuthAndCertificatesToGlobal(t *testing.T) {
	root := legacyWorkspace(t)
	s := New(root)
	notes, err := s.Migrate()
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) == 0 {
		t.Fatal("a legacy workspace must report what was changed")
	}

	// The environment list becomes global.
	g, err := s.GetGlobal()
	if err != nil || strings.Join(g.Environments, ",") != "stage" || strings.Join(g.Regions, ",") != "AU,NA" || g.DefaultEnvironment != "stage/NA" {
		t.Fatalf("global settings: %+v %v", g, err)
	}
	if _, err := os.Stat(filepath.Join(root, "global", "settings.yaml")); err != nil {
		t.Fatal("settings.yaml must be written so the list no longer depends on the applications")
	}

	// billing is moved first (alphabetical), so it keeps the plain names and
	// orders' clashing items get the application prefix.
	billingToken, _ := s.GetAuth("token")
	ordersToken, err := s.GetAuth("orders-token")
	if err != nil || billingToken.Name != "Billing token" || ordersToken.Name != "Orders token" {
		t.Fatalf("clashing profiles must both survive: %+v / %+v (%v)", billingToken, ordersToken, err)
	}
	raw := read(t, root, "global/auth/orders-token.yaml")
	for _, want := range []string{"# how orders gets its token", "id: orders-token", "'{{orders.baseUrl}}/oauth/token' # issued by orders itself", "{{$env.ORDERS_SECRET}}"} {
		if !strings.Contains(raw, want) {
			t.Errorf("moved profile lacks %q:\n%s", want, raw)
		}
	}
	// Identical data is one profile, whatever the layout of the two files.
	if list, _ := s.ListAuth(); len(list) != 3 {
		t.Errorf("want token, orders-token and one plain, got %d: %+v", len(list), list)
	}

	// Certificate files: identical ones are merged, different ones renamed
	// and the documents that named them follow.
	if read(t, root, "global/certs/a.crt") != "BILLING CERT" || read(t, root, "global/certs/orders-a.crt") != "ORDERS CERT" || read(t, root, "global/certs/a.key") != "ORDERS KEY" {
		t.Error("certificate files were not moved as expected")
	}
	if _, err := os.Stat(filepath.Join(root, "global", "certs", "orders-a.key")); err == nil {
		t.Error("an identical key file must not be duplicated")
	}
	ordersSet, err := s.GetCertSet("orders-client")
	if err != nil || ordersSet.Cert != "orders-a.crt" || ordersSet.Key != "a.key" || ordersSet.Hosts[0] != "{{orders.baseUrl}}" {
		t.Fatalf("orders' certificate set: %+v %v", ordersSet, err)
	}
	if c, _ := s.GetCertSet("client"); c.Name != "Billing client" || c.Environments["stage"].CA != "a.crt" {
		t.Fatalf("billing's certificate set: %+v", c)
	}

	// References follow, and the rest of each file is untouched.
	flow := read(t, root, "applications/orders/scenarios/flow.yaml")
	for _, want := range []string{"mtls: orders-client", "auth: orders-token # default profile", "auth: none"} {
		if !strings.Contains(flow, want) {
			t.Errorf("orders/flow lacks %q:\n%s", want, flow)
		}
	}
	pay := read(t, root, "applications/billing/scenarios/pay.yaml")
	for _, want := range []string{"auth: orders-token   # borrowed from orders", `mtls: "orders-client"`, "    auth: token\n    mtls: client\n", "auth: plain}"} {
		if !strings.Contains(pay, want) {
			t.Errorf("billing/pay lacks %q:\n%s", want, pay)
		}
	}
	if app := read(t, root, "applications/orders/application.yaml"); !strings.Contains(app, "defaultAuth: orders-token # the usual one") {
		t.Errorf("default auth must follow the rename:\n%s", app)
	}
	if a, _ := s.GetApp("billing"); a.DefaultAuth != "token" {
		t.Errorf("billing's default auth: %q", a.DefaultAuth)
	}
	for _, sc := range []struct{ app, id string }{{"orders", "flow"}, {"billing", "pay"}} {
		got, err := s.GetScenario(sc.app, sc.id)
		if err != nil {
			t.Fatalf("%s/%s no longer parses: %v", sc.app, sc.id, err)
		}
		auths, sets := scenarioRefs(got)
		for _, id := range auths {
			if _, err := s.GetAuth(id); err != nil {
				t.Errorf("%s/%s refers to auth %q, which does not exist", sc.app, sc.id, id)
			}
		}
		for _, id := range sets {
			if _, err := s.GetCertSet(id); err != nil {
				t.Errorf("%s/%s refers to certificate set %q, which does not exist", sc.app, sc.id, id)
			}
		}
	}

	// Nothing is left inside the applications.
	for _, app := range []string{"orders", "billing"} {
		for _, d := range []string{"auth", "tls", "certs"} {
			if _, err := os.Stat(filepath.Join(root, "applications", app, "assets", d)); err == nil {
				t.Errorf("%s/assets/%s should be gone", app, d)
			}
		}
	}

	// Running it again changes nothing.
	again, err := s.Migrate()
	if err != nil || len(again) != 0 {
		t.Errorf("second run: %v %v", again, err)
	}

	u, err := s.Usage()
	if err != nil || strings.Join(u.Auth["orders-token"], ",") != "billing/pay,orders (default),orders/flow" && strings.Join(u.Auth["orders-token"], ",") != "orders (default),orders/flow,billing/pay" {
		t.Errorf("usage of orders-token: %v %v", u.Auth["orders-token"], err)
	}
}

func TestMigrateLeavesACurrentWorkspaceAlone(t *testing.T) {
	s := newStore(t)
	_ = s.SaveGlobal(model.DefaultGlobal())
	_ = s.SaveAuth(&model.AuthProfile{ID: "token", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "x"}})
	sc := scenario()
	sc.Auth = "token"
	_ = s.SaveScenario("shop", sc)
	before := read(t, s.Root, "applications/shop/scenarios/orders.yaml")
	notes, err := s.Migrate()
	if err != nil || len(notes) != 0 {
		t.Fatalf("nothing to do, yet: %v %v", notes, err)
	}
	if read(t, s.Root, "applications/shop/scenarios/orders.yaml") != before {
		t.Error("scenario was rewritten")
	}
}

func TestGlobalSettings(t *testing.T) {
	// A workspace with nothing to go by starts with the standard list.
	s := New(t.TempDir())
	g, err := s.GetGlobal()
	if err != nil || strings.Join(g.Keys(), ",") != "stage/NA,stage/AU,qa/NA,qa/AU,prod/NA,prod/AU" || g.DefaultEnvironment != "stage/NA" {
		t.Fatalf("default: %+v %v", g, err)
	}
	// A backend pins the list when it starts, so a new workspace keeps the
	// standard one whatever its first application defines.
	pinned := New(t.TempDir())
	if err := pinned.EnsureGlobal(); err != nil {
		t.Fatal(err)
	}
	_ = pinned.SaveApp(&model.Application{ID: "a", Environments: map[string]map[string]string{"stage/NA": {}}})
	if g, _ := pinned.GetGlobal(); len(g.Keys()) != 6 {
		t.Fatalf("pinned list changed: %+v", g)
	}
	// Without the file, the list follows what the applications use.
	_ = s.SaveApp(&model.Application{ID: "a", DefaultEnvironment: "dev", Environments: map[string]map[string]string{"dev": {}, "staging": {}}})
	if g, _ = s.GetGlobal(); strings.Join(g.Keys(), ",") != "dev,staging" || g.DefaultEnvironment != "dev" {
		t.Fatalf("derived: %+v", g)
	}
	for name, bad := range map[string]*model.Global{
		"no environment":  {Regions: []string{"NA"}},
		"bad name":        {Environments: []string{"stage/NA"}},
		"duplicate":       {Environments: []string{"stage", "stage"}},
		"env and region":  {Environments: []string{"NA"}, Regions: []string{"NA"}},
		"unknown default": {Environments: []string{"stage"}, DefaultEnvironment: "prod"},
		"default no pair": {Environments: []string{"stage"}, Regions: []string{"NA"}, DefaultEnvironment: "stage"},
	} {
		if err := s.SaveGlobal(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := &model.Global{Environments: []string{"stage", "prod"}, Regions: []string{"NA", "EU"}, Values: map[string]map[string]string{"stage/EU": {"idp": "x"}, "prod": {}}}
	if err := s.SaveGlobal(ok); err != nil {
		t.Fatal(err)
	}
	if g, _ = s.GetGlobal(); g.DefaultEnvironment != "stage/NA" || g.Values["stage/EU"]["idp"] != "x" || len(g.Values) != 1 {
		t.Fatalf("saved: %+v", g)
	}
	vals := model.LayeredValues(map[string]map[string]string{"stage": {"a": "env", "b": "env"}, "EU": {"b": "region", "c": "region"}, "stage/EU": {"c": "pair"}, "prod/EU": {"a": "other"}}, "stage/EU")
	if vals["a"] != "env" || vals["b"] != "region" || vals["c"] != "pair" {
		t.Errorf("environment, then region, then the pair: %v", vals)
	}
}
