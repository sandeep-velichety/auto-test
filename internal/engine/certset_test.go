package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/demo"
	"github.com/testplane/testplane/internal/model"
)

// Certificates are configured once per application as certificate sets; a
// test chooses whether to present one, independently of its auth profile,
// and both can differ per environment.
func TestCertificateSetChosenPerTest(t *testing.T) {
	e, st := fixture(t) // app "shop": env "test" works, env "other" points at a closed port
	shop, _ := st.GetApp("shop")
	shop.Environments["staging"] = map[string]string{"baseUrl": shop.Environments["test"]["baseUrl"], "secureUrl": shop.Environments["test"]["secureUrl"]}
	must(t, st.SaveApp(shop))
	dir := st.CertsDir()
	// A second, unrelated key pair: a valid certificate the server does not trust.
	stranger, _ := demo.NewCerts()
	must(t, os.WriteFile(filepath.Join(dir, "stranger.crt"), stranger.ClientCert, 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "stranger.key"), stranger.ClientKey, 0o600))

	must(t, st.SaveCertSet(&model.CertSet{ID: "client", Name: "Client", Cert: "demo-client.crt", Key: "demo-client.key", CA: "demo-ca.crt",
		Environments: map[string]model.TLSSettings{"staging": {Cert: "stranger.crt", Key: "stranger.key"}}}))
	must(t, st.SaveCertSet(&model.CertSet{ID: "trust", Name: "Trust only", CA: "demo-ca.crt"}))
	// The token profile differs per environment too.
	must(t, st.SaveAuth(&model.AuthProfile{ID: "token", Type: model.AuthOAuth2ClientCredentials,
		OAuth2:       &model.OAuth2Auth{TokenURL: "{{baseUrl}}/oauth/token", ClientID: demo.ClientID, ClientSecret: demo.ClientSecret},
		Environments: map[string]map[string]any{"staging": {"oauth2": map[string]any{"clientSecret": "wrong-in-staging"}}}}))
	must(t, st.SaveAuth(&model.AuthProfile{ID: "tenant", Type: model.AuthHeaders, Headers: map[string]string{"X-Tenant": "acme"}}))

	echo := func() *model.HTTPStep { return &model.HTTPStep{URL: "{{secureUrl}}/secure/echo"} }
	save(t, st, &model.Scenario{ID: "certs", Auth: "tenant", MTLS: "client", Tests: []model.Test{
		{ID: "inherits-set", Type: model.TypeHTTP, ContinueOnFail: true, HTTP: echo(),
			Expect: &model.Expect{Body: []model.Assertion{{Path: "client", Value: "testplane-demo-client"}, {Path: "headers.X-Tenant", Value: "acme"}}},
			Variants: []model.Variant{
				{ID: "no-cert", MTLS: "trust", Expect: &model.Expect{Error: "*"}},
				{ID: "nothing", MTLS: "none", Expect: &model.Expect{Error: "certificate"}},
				{ID: "no-auth-still-cert", Auth: "none", Expect: &model.Expect{Status: intp(400)}},
			}},
		{ID: "legacy-profile-cert", Type: model.TypeHTTP, ContinueOnFail: true, Auth: "mtls", MTLS: "none", Kind: model.KindNegative, HTTP: echo(), Expect: &model.Expect{Error: "unknown authority"}},
		{ID: "token-and-cert", Type: model.TypeHTTP, ContinueOnFail: true, Auth: "token", HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/1"}, Expect: &model.Expect{Status: intp(404)}},
		{ID: "unknown-set", Type: model.TypeHTTP, ContinueOnFail: true, MTLS: "ghost", HTTP: echo()},
	}})
	ctx := context.Background()

	res, err := e.Run(ctx, Options{App: "shop", Env: "test"})
	must(t, err)
	want := map[string]string{"inherits-set": "passed", "inherits-set.no-cert": "passed", "inherits-set.nothing": "passed", "inherits-set.no-auth-still-cert": "passed",
		"legacy-profile-cert": "passed", "token-and-cert": "passed", "unknown-set": "error"}
	got := statuses(res)
	for id, w := range want {
		if got[id] != w {
			t.Errorf("env test, %s: %s, want %s (%s)", id, got[id], w, find(t, res, id).Error)
		}
	}
	if r := find(t, res, "inherits-set"); r.MTLS != "client" || r.Auth != "tenant" {
		t.Errorf("result should record what was used: auth=%q mtls=%q", r.Auth, r.MTLS)
	}
	if r := find(t, res, "inherits-set.nothing"); !strings.Contains(strings.ToLower(r.Assertions[0].Actual), "unknown authority") {
		t.Errorf("mtls none drops the CA too, so the server is not trusted: %+v", r.Assertions)
	}
	if r := find(t, res, "inherits-set.no-cert"); strings.Contains(strings.ToLower(r.Assertions[0].Actual), "unknown authority") {
		t.Errorf("a CA-only set trusts the server and is refused for presenting nothing: %+v", r.Assertions)
	}
	if !strings.Contains(find(t, res, "unknown-set").Error, `certificate set "ghost"`) {
		t.Errorf("unknown set message: %s", find(t, res, "unknown-set").Error)
	}

	// Staging uses another certificate and another client secret.
	res, err = e.Run(ctx, Options{App: "shop", Env: "staging"})
	must(t, err)
	got = statuses(res)
	if got["inherits-set"] != model.StatusError || got["token-and-cert"] != model.StatusError {
		t.Fatalf("staging should fail with its own certificate and secret: %v", got)
	}
	if msg := find(t, res, "token-and-cert").Error; !strings.Contains(msg, "answered 401") {
		t.Errorf("staging secret: %s", msg)
	}

	// The check used by the UI's "Try it" follows the environment as well.
	ok, err := e.CheckAuth(ctx, "shop", "token", "test", "client")
	must(t, err)
	if !ok.OK || !ok.ClientCert {
		t.Errorf("check in test: %+v", ok)
	}
	bad, err := e.CheckAuth(ctx, "shop", "token", "staging", "")
	must(t, err)
	if bad.OK {
		t.Errorf("check in staging should fail: %+v", bad)
	}
}

// Certificate sets and auth profiles are global: an application that owns
// neither uses them by plain id.
func TestCertificateSetIsUsableByAnyApplication(t *testing.T) {
	e, st := fixture(t)
	must(t, st.SaveCertSet(&model.CertSet{ID: "client", Cert: "demo-client.crt", Key: "demo-client.key", CA: "demo-ca.crt"}))
	must(t, st.SaveApp(&model.Application{ID: "team", Name: "Team", DefaultEnvironment: "test", Environments: map[string]map[string]string{"test": {}}}))
	must(t, st.SaveScenario("team", &model.Scenario{ID: "flow", Name: "Flow", Tests: []model.Test{
		{ID: "ping", Name: "Ping", Type: model.TypeHTTP, MTLS: "client", HTTP: &model.HTTPStep{URL: "{{shop.secureUrl}}/secure/ping"}},
		{ID: "with-token", Name: "Token", Type: model.TypeHTTP, Auth: "oauth", MTLS: "client", HTTP: &model.HTTPStep{URL: "{{shop.baseUrl}}/orders/1"}, Expect: &model.Expect{Status: intp(404)}},
		{ID: "old-style", Name: "Old reference", Type: model.TypeHTTP, MTLS: "shop/client", ContinueOnFail: true, HTTP: &model.HTTPStep{URL: "{{shop.secureUrl}}/secure/ping"}},
	}}))
	res, err := e.Run(context.Background(), Options{App: "team"})
	must(t, err)
	for id, s := range statuses(res) {
		if want := map[bool]string{true: model.StatusError, false: model.StatusPassed}[id == "old-style"]; s != want {
			t.Errorf("%s: %s, want %s (%s)", id, s, want, find(t, res, id).Error)
		}
	}
}

// Like client certificates in Postman: a set registered for a host is used
// automatically for calls to that host.
func TestCertificateSetMatchedByHost(t *testing.T) {
	e, st := fixture(t)
	shop, _ := st.GetApp("shop")
	secure := strings.TrimPrefix(shop.Environments["test"]["secureUrl"], "https://")
	shop.Environments["test"]["secureHost"] = secure
	must(t, st.SaveApp(shop))
	must(t, st.SaveCertSet(&model.CertSet{ID: "client", Hosts: []string{"unrelated.example.com", "{{shop.secureHost}}"}, Cert: "demo-client.crt", Key: "demo-client.key", CA: "demo-ca.crt"}))
	must(t, st.SaveCertSet(&model.CertSet{ID: "trust", CA: "demo-ca.crt"}))
	ping := &model.HTTPStep{URL: "{{secureUrl}}/secure/ping"}
	save(t, st, &model.Scenario{ID: "auto", Auth: "none", Tests: []model.Test{
		{ID: "automatic", Type: model.TypeHTTP, HTTP: ping, Variants: []model.Variant{
			{ID: "turned-off", MTLS: "none", Expect: &model.Expect{Error: "certificate"}},
			{ID: "explicit-other", MTLS: "trust", Expect: &model.Expect{Error: "*"}},
			// A profile with certificate files of its own is not overridden
			// by the host match: this one trusts the CA and presents nothing.
			{ID: "profile-files-win", Auth: "ca-only", Expect: &model.Expect{Error: "*"}},
		}},
		{ID: "other-host", Type: model.TypeHTTP, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/health"}},
	}})
	res, err := e.Run(context.Background(), Options{App: "shop"})
	must(t, err)
	for id, s := range statuses(res) {
		if s != model.StatusPassed {
			t.Errorf("%s: %s (%s)", id, s, find(t, res, id).Error)
		}
	}
	if got := find(t, res, "automatic.profile-files-win").MTLS; got != "" {
		t.Errorf("the profile's own files were used, not a set: %q", got)
	}
	// The set is global: another application calling that host gets it too.
	must(t, st.SaveApp(&model.Application{ID: "team", Name: "Team", DefaultEnvironment: "test"}))
	must(t, st.SaveScenario("team", &model.Scenario{ID: "ping", Name: "Ping", Tests: []model.Test{
		{ID: "ping", Name: "Ping", Type: model.TypeHTTP, HTTP: &model.HTTPStep{URL: "{{shop.secureUrl}}/secure/ping"}},
	}}))
	teamRes, err := e.Run(context.Background(), Options{App: "team"})
	must(t, err)
	if r := find(t, teamRes, "ping"); r.Status != model.StatusPassed || r.MTLS != "client" {
		t.Errorf("another application calling the same host: %s, set %q (%s)", r.Status, r.MTLS, r.Error)
	}
	if got := find(t, res, "automatic").MTLS; got != "client" {
		t.Errorf("matched set should be recorded: %q", got)
	}
	if got := find(t, res, "other-host").MTLS; got != "" {
		t.Errorf("a host with no registered set presents nothing: %q", got)
	}
	if got := find(t, res, "automatic.turned-off").MTLS; got != "none" {
		t.Errorf("explicit none wins over the host match: %q", got)
	}
}
