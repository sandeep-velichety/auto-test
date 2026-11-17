package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/model"
	"github.com/testplane/testplane/internal/store"
)

// A backend can be limited to one environment: the stage backend sits
// where it can reach stage and holds stage's certificates, so it refuses
// to run anything else, whatever is asked or configured as a default.
func TestBackendLimitedToOneEnvironment(t *testing.T) {
	e, st := fixture(t)
	shop, _ := st.GetApp("shop")
	api := shop.Environments["test"]["baseUrl"]
	must(t, st.SaveGlobal(&model.Global{Environments: []string{"stage", "prod"}, Regions: []string{"NA", "AU"}, DefaultEnvironment: "prod/NA"}))
	shop.DefaultEnvironment, shop.DefaultAuth = "prod/AU", ""
	shop.Environments = map[string]map[string]string{"stage": {"baseUrl": api}, "prod": {"baseUrl": api}}
	must(t, st.SaveApp(shop))
	must(t, st.SaveScenario("shop", &model.Scenario{ID: "call", Name: "Call", Tests: []model.Test{{ID: "get", Name: "Get", Type: model.TypeHTTP,
		HTTP: &model.HTTPStep{URL: "{{baseUrl}}/orders/1"}, Expect: &model.Expect{Status: intp(404)}}}}))
	ctx := context.Background()
	ran := func(env string) (string, error) {
		res, err := e.Run(ctx, Options{App: "shop", Env: env})
		if err != nil {
			return "", err
		}
		return res.Environment, nil
	}

	// Not limited: defaults apply as before.
	if got, err := ran(""); err != nil || got != "prod/AU" {
		t.Fatalf("unlimited backend, application default: %q %v", got, err)
	}

	// Limited to one pair: that pair is what runs when nothing is asked,
	// even though the application and global defaults point at prod.
	e.Scope = "stage/NA"
	if got, err := ran(""); err != nil || got != "stage/NA" {
		t.Fatalf("stage/NA backend with prod defaults: %q %v", got, err)
	}
	if got, err := ran("stage/NA"); err != nil || got != "stage/NA" {
		t.Fatalf("stage/NA asked: %q %v", got, err)
	}
	for _, other := range []string{"prod/NA", "stage/AU", "prod/AU"} {
		_, err := ran(other)
		if !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "runs stage/NA only") {
			t.Errorf("%s on the stage/NA backend must be refused: %v", other, err)
		}
	}
	// Trying an auth profile is a call to real endpoints too.
	must(t, st.SaveAuth(&model.AuthProfile{ID: "key", Type: model.AuthHeaders, Headers: map[string]string{"X-Key": "k"}}))
	if _, err := e.CheckAuth(ctx, "", "key", "prod/NA", ""); err == nil {
		t.Error("trying a profile against prod on the stage backend must be refused")
	}
	if c, err := e.CheckAuth(ctx, "", "key", "", ""); err != nil || !c.OK {
		t.Errorf("trying a profile with no environment uses the backend's own: %+v %v", c, err)
	}

	// Limited to an environment: every region of it, nothing else.
	e.Scope = "prod"
	if got, err := ran(""); err != nil || got != "prod/AU" {
		t.Fatalf("prod backend, application default prod/AU: %q %v", got, err)
	}
	if got, err := ran("prod/NA"); err != nil || got != "prod/NA" {
		t.Fatalf("prod backend, prod/NA: %q %v", got, err)
	}
	if _, err := ran("stage/NA"); err == nil {
		t.Error("stage on the prod backend must be refused")
	}
}
