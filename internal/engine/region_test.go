package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/demo"
	"github.com/testplane/testplane/internal/model"
)

func TestEnvLayers(t *testing.T) {
	for in, want := range map[string]string{"": "", "stage": "stage", "stage/NA": "stage,NA,stage/NA", "/NA": "/NA", "stage/": "stage/"} {
		if got := strings.Join(model.EnvLayers(in), ","); got != want {
			t.Errorf("EnvLayers(%q) = %q, want %q", in, got, want)
		}
	}
	c := model.CertSet{ID: "c", Cert: "base.crt", Key: "base.key", Environments: map[string]model.TLSSettings{
		"prod": {CA: "prod-ca.pem"}, "AU": {Cert: "au.crt", Key: "au.key"}, "prod/AU": {ServerName: "au.prod"}}}
	if got := *c.For("prod/AU"); got != (model.TLSSettings{Cert: "au.crt", Key: "au.key", CA: "prod-ca.pem", ServerName: "au.prod"}) {
		t.Errorf("environment, region and pair overrides should all apply: %+v", got)
	}
	if got := *c.For("stage/NA"); got != (model.TLSSettings{Cert: "base.crt", Key: "base.key"}) {
		t.Errorf("a pair with no overrides uses the defaults: %+v", got)
	}
}

// Environments as environment/region pairs: each pair has its own values,
// and auth can differ by environment, by region or by pair.
func TestEnvironmentRegionMatrix(t *testing.T) {
	e, st := fixture(t)
	shop, _ := st.GetApp("shop")
	good := shop.Environments["test"]["baseUrl"]
	shop.Environments = map[string]map[string]string{
		"stage/NA": {"baseUrl": good, "greeting": "na"},
		"stage/AU": {"baseUrl": good, "greeting": "au"},
		"prod/NA":  {"baseUrl": "http://127.0.0.1:1", "greeting": "prod"},
	}
	shop.DefaultEnvironment = "stage/NA"
	must(t, st.SaveApp(shop))
	must(t, st.SaveAuth(&model.AuthProfile{ID: "token", Type: model.AuthOAuth2ClientCredentials,
		OAuth2: &model.OAuth2Auth{TokenURL: "{{baseUrl}}/oauth/token", ClientID: demo.ClientID, ClientSecret: "wrong-everywhere"},
		Environments: map[string]map[string]any{
			"stage": {"oauth2": map[string]any{"clientSecret": demo.ClientSecret}}, // right for all of stage
			"AU":    {"headers": map[string]any{"X-Region": "au"}},                 // extra header in AU only
		}}))
	save(t, st, &model.Scenario{ID: "matrix", Auth: "token", Tests: []model.Test{
		{ID: "call", Type: model.TypeHTTP, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/1", Headers: map[string]string{"X-Where": "{{$environment}}-{{$region}}-{{greeting}}"}}, Expect: &model.Expect{Status: intp(404)}},
	}})
	ctx := context.Background()
	for env, where := range map[string]string{"": "stage-NA-na", "stage/AU": "stage-AU-au"} {
		res, err := e.Run(ctx, Options{App: "shop", Env: env})
		must(t, err)
		r := find(t, res, "call")
		if r.Status != model.StatusPassed || r.Request.(map[string]any)["headers"].(map[string]any)["X-Where"] != where {
			t.Errorf("env %q: %s %v (%s)", env, r.Status, r.Request, r.Error)
		}
		if env != "" && res.Environment != env {
			t.Errorf("run should record the pair: %q", res.Environment)
		}
	}
	// prod has neither a reachable address nor the stage secret.
	res, err := e.Run(ctx, Options{App: "shop", Env: "prod/NA"})
	must(t, err)
	if find(t, res, "call").Status != model.StatusError {
		t.Error("prod/NA should fail")
	}
	if _, err := e.Run(ctx, Options{App: "shop", Env: "stage/EU"}); err == nil {
		t.Error("a pair that is not defined must be an error")
	}
	// The AU-only header is part of the credentials there, and nowhere else.
	au, err := e.CheckAuth(ctx, "shop", "token", "stage/AU", "")
	must(t, err)
	na, err := e.CheckAuth(ctx, "shop", "token", "stage/NA", "")
	must(t, err)
	if !au.OK || strings.Join(au.Sends, ",") != "header Authorization,header X-Region" || !na.OK || strings.Join(na.Sends, ",") != "header Authorization" {
		t.Errorf("AU: %+v / NA: %+v", au, na)
	}
}
