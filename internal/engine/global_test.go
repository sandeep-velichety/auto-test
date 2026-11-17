package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/demo"
	"github.com/testplane/testplane/internal/model"
)

// One global auth profile, with its values per environment and region, is
// used by two applications. The environment list and shared values are
// global; each application only adds its own values.
func TestGlobalAuthPerEnvironmentAndRegionAcrossApplications(t *testing.T) {
	e, st := fixture(t)
	shop, _ := st.GetApp("shop")
	api := shop.Environments["test"]["baseUrl"]

	must(t, st.SaveGlobal(&model.Global{
		Environments: []string{"stage", "prod"}, Regions: []string{"NA", "AU"}, DefaultEnvironment: "stage/NA",
		Values: map[string]map[string]string{
			"stage":    {"idpUrl": api, "greeting": "shared"}, // every region of stage
			"AU":       {"greeting": "shared-au"},             // every environment in AU
			"prod/NA":  {"idpUrl": "http://127.0.0.1:1"},      // one exact pair
			"stage/AU": {"only": "stage-au"},
		}}))
	// The profile is complete on its own: nothing in it belongs to an application.
	must(t, st.SaveAuth(&model.AuthProfile{ID: "idp", Name: "Identity provider", Type: model.AuthOAuth2ClientCredentials,
		OAuth2: &model.OAuth2Auth{TokenURL: "{{idpUrl}}/oauth/token", ClientID: demo.ClientID, ClientSecret: "wrong-by-default"},
		Environments: map[string]map[string]any{
			"stage":    {"oauth2": map[string]any{"clientSecret": demo.ClientSecret}},
			"stage/AU": {"headers": map[string]any{"X-Region": "au"}},
		}}))

	// shop keeps values per environment; billing has one value for NA and
	// overrides a shared one.
	shop.DefaultEnvironment, shop.DefaultAuth = "", "idp"
	shop.Environments = map[string]map[string]string{"stage": {"baseUrl": api}, "prod": {"baseUrl": api}}
	must(t, st.SaveApp(shop))
	must(t, st.SaveApp(&model.Application{ID: "billing", Name: "Billing", DefaultAuth: "idp",
		Environments: map[string]map[string]string{"NA": {"baseUrl": api, "greeting": "billing-na"}, "AU": {"baseUrl": api}}}))
	sc := &model.Scenario{ID: "call", Name: "Call", Tests: []model.Test{{ID: "get", Name: "Get", Type: model.TypeHTTP,
		HTTP:   &model.HTTPStep{URL: "{{baseUrl}}/orders/1", Headers: map[string]string{"X-Where": "{{$environment}}-{{$region}}-{{greeting}}"}},
		Expect: &model.Expect{Status: intp(404)}}}}
	must(t, st.SaveScenario("shop", sc))
	must(t, st.SaveScenario("billing", sc))

	ctx := context.Background()
	for _, c := range []struct{ app, env, where, status string }{
		{"shop", "", "stage-NA-shared", model.StatusPassed},            // no environment anywhere: the global default
		{"shop", "stage/AU", "stage-AU-shared-au", model.StatusPassed}, // region value over environment value
		{"billing", "stage/NA", "stage-NA-billing-na", model.StatusPassed},
		{"billing", "stage/AU", "stage-AU-shared-au", model.StatusPassed},
		{"shop", "prod/NA", "", model.StatusError}, // prod has neither the secret nor a reachable identity provider
	} {
		res, err := e.Run(ctx, Options{App: c.app, Env: c.env})
		must(t, err)
		r := find(t, res, "get")
		if r.Status != c.status {
			t.Errorf("%s in %q: %s, want %s (%s)", c.app, c.env, r.Status, c.status, r.Error)
			continue
		}
		if c.where != "" && r.Request.(map[string]any)["headers"].(map[string]any)["X-Where"] != c.where {
			t.Errorf("%s in %q: %v, want %s", c.app, c.env, r.Request, c.where)
		}
		if r.Auth != "idp" {
			t.Errorf("%s: auth %q", c.app, r.Auth)
		}
	}

	// The list is global: a pair outside it is refused for every application.
	for _, env := range []string{"qa/NA", "stage", "stage/EU"} {
		if _, err := e.Run(ctx, Options{App: "billing", Env: env}); err == nil || !strings.Contains(err.Error(), "global list") {
			t.Errorf("environment %q: %v", env, err)
		}
	}

	// Trying the profile needs no application, and reflects the pair asked for.
	au, err := e.CheckAuth(ctx, "", "idp", "stage/AU", "")
	must(t, err)
	na, err := e.CheckAuth(ctx, "", "idp", "", "")
	must(t, err)
	prod, err := e.CheckAuth(ctx, "", "idp", "prod/AU", "")
	must(t, err)
	if !au.OK || strings.Join(au.Sends, ",") != "header Authorization,header X-Region" || !na.OK || strings.Join(na.Sends, ",") != "header Authorization" {
		t.Errorf("AU: %+v / NA: %+v", au, na)
	}
	if prod.OK || !strings.Contains(prod.Error, "idpUrl") {
		t.Errorf("prod/AU has no idpUrl: %+v", prod)
	}
}
