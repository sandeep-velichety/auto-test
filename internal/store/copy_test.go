package store

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/model"
)

// Copying an application from the stage backend to the prod backend:
// scenarios and settings travel, auth and certificates do not, and prod
// keeps the values that describe its own surroundings.
func TestCopyApplicationToAnotherBackend(t *testing.T) {
	stage := New(t.TempDir())
	_ = stage.SaveGlobal(&model.Global{Environments: []string{"stage", "prod"}, Regions: []string{"NA", "AU"}})
	_ = stage.SaveAuth(&model.AuthProfile{ID: "idp", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "stage-secret"}})
	_ = stage.SaveCertSet(&model.CertSet{ID: "client", Cert: "c.crt", Key: "c.key"})
	_ = stage.SaveCert("c.crt", strings.NewReader("CERT"), false)
	_ = stage.SaveApp(&model.Application{ID: "orders", Name: "Orders", DefaultEnvironment: "stage/NA", DefaultAuth: "idp", Description: "from stage",
		Environments: map[string]map[string]string{
			"stage/NA": {"baseUrl": "https://orders.na.stage"},
			"prod/NA":  {"baseUrl": "https://guessed-on-stage", "newVar": "added-on-stage"},
			"prod/AU":  {"baseUrl": "https://orders.au.prod"},
		}})
	sc := &model.Scenario{ID: "flow", Name: "Flow", Auth: "idp", MTLS: "client", Tests: []model.Test{{ID: "t", Name: "T", Type: model.TypeHTTP, HTTP: &model.HTTPStep{URL: "{{baseUrl}}/x"}}}}
	if err := stage.SaveScenario("orders", sc); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := stage.ExportAppOnly("orders", &buf); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	for _, f := range zr.File {
		if strings.Contains(f.Name, "/global/") {
			t.Fatalf("auth and certificates must not travel: %s", f.Name)
		}
	}

	// prod has the application already, with its real address, and its own
	// version of the auth profile; it has no certificate set yet.
	prod := New(t.TempDir())
	_ = prod.SaveGlobal(&model.Global{Environments: []string{"stage", "prod"}, Regions: []string{"NA", "AU"}})
	_ = prod.SaveAuth(&model.AuthProfile{ID: "idp", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "prod-secret"}})
	_ = prod.SaveApp(&model.Application{ID: "orders", Name: "Orders", DefaultEnvironment: "prod/NA", Description: "old",
		Environments: map[string]map[string]string{"prod/NA": {"baseUrl": "https://orders.na.prod"}, "NA": {"dc": "iad"}}})

	if _, err := prod.ImportAppFor(buf.Bytes(), false, "prod/NA"); err == nil {
		t.Fatal("an existing application is updated only on request")
	}
	if _, err := prod.ImportAppFor(buf.Bytes(), true, "prod/NA"); err != nil {
		t.Fatal(err)
	}
	a, _ := prod.GetApp("orders")
	if a.Description != "from stage" {
		t.Errorf("settings come from the copy: %+v", a)
	}
	if got := a.Environments["prod/NA"]; got["baseUrl"] != "https://orders.na.prod" || got["newVar"] != "added-on-stage" {
		t.Errorf("prod keeps its own values for prod/NA and gains new variables: %v", got)
	}
	if a.Environments["NA"]["dc"] != "iad" || a.Environments["prod/AU"]["baseUrl"] != "https://orders.au.prod" || a.Environments["stage/NA"]["baseUrl"] != "https://orders.na.stage" {
		t.Errorf("region-wide values are kept, other environments come from the copy: %v", a.Environments)
	}
	if a.DefaultEnvironment != "prod/NA" {
		t.Errorf("prod keeps its default environment: %q", a.DefaultEnvironment)
	}
	if got, err := prod.GetScenario("orders", "flow"); err != nil || got.Auth != "idp" {
		t.Fatalf("scenario after copy: %+v %v", got, err)
	}
	if p, _ := prod.GetAuth("idp"); p.Bearer.Token != "prod-secret" {
		t.Error("prod's own auth profile must be untouched")
	}
	if got := strings.Join(prod.MissingRefs("orders"), ", "); got != "certificate set client" {
		t.Errorf("what prod still has to set up: %q", got)
	}

	// A single scenario copied as plain YAML reports the same gap.
	other := New(t.TempDir())
	_ = other.SaveApp(&model.Application{ID: "orders", Name: "Orders"})
	raw, err := stage.GetScenarioYAML("orders", "flow")
	if err != nil {
		t.Fatal(err)
	}
	res, err := other.ImportScenario("orders", raw, false)
	if err != nil || strings.Join(res.Missing, ", ") != "auth profile idp, certificate set client" {
		t.Fatalf("copied scenario: %+v %v", res, err)
	}
}
