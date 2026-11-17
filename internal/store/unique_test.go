package store

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/model"
)

func conflictWith(t *testing.T, err error, kind, existing string) {
	t.Helper()
	var c *ConflictError
	if !errors.Is(err, ErrConflict) || !errors.As(err, &c) {
		t.Fatalf("want a conflict naming the existing %s, got %v", kind, err)
	}
	if c.Kind != kind || c.Existing != existing {
		t.Fatalf("conflict names %s %q, want %s %q (%v)", c.Kind, c.Existing, kind, existing, err)
	}
}

// Names are unique within their kind. Saving under the same id is an
// update and always allowed; a second item with a taken name is refused
// and the error says which item has it. Names are compared exactly.
func TestNamesAreUnique(t *testing.T) {
	s := newStore(t) // has application "shop"
	shop, _ := s.GetApp("shop")

	// applications
	conflictWith(t, s.SaveApp(&model.Application{ID: "shop-2", Name: shop.Name}), "application", "shop")
	if _, err := s.GetApp("shop-2"); err == nil {
		t.Fatal("a refused application must not be created")
	}
	if err := s.SaveApp(&model.Application{ID: "other", Name: strings.ToLower(shop.Name) + " "}); err != nil {
		t.Fatalf("names are compared exactly, so a different spelling is another name: %v", err)
	}
	shop.Description = "updated"
	if err := s.SaveApp(shop); err != nil {
		t.Fatalf("updating an application under its own name: %v", err)
	}
	other, _ := s.GetApp("other")
	other.Name = shop.Name
	conflictWith(t, s.SaveApp(other), "application", "shop") // renaming onto a taken name

	// scenarios: unique inside one application, free across applications
	sc := scenario()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SaveScenario("shop", sc))
	must(s.SaveScenario("shop", sc)) // update
	twin := scenario()
	twin.ID = "orders-again"
	conflictWith(t, s.SaveScenario("shop", twin), "scenario", sc.ID)
	_, err := s.SaveScenarioYAML("shop", "orders-again", []byte("id: orders-again\nname: "+sc.Name+"\n"))
	conflictWith(t, err, "scenario", sc.ID)
	must(s.SaveScenario("other", twin))

	// tests inside a scenario
	dup := scenario()
	dup.Tests = append(dup.Tests, dup.Tests[0])
	dup.Tests[1].ID = "second"
	if err := s.SaveScenario("shop", dup); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "already used by test #1") {
		t.Fatalf("two tests with one name in a scenario: %v", err)
	}

	// auth profiles and certificate sets (global)
	must(s.SaveAuth(&model.AuthProfile{ID: "token", Name: "Company token", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "a"}}))
	must(s.SaveAuth(&model.AuthProfile{ID: "token", Name: "Company token", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "b"}}))
	conflictWith(t, s.SaveAuth(&model.AuthProfile{ID: "token-2", Name: "Company token", Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "c"}}), "auth profile", "token")
	if p, _ := s.GetAuth("token"); p.Bearer.Token != "b" {
		t.Fatalf("the update went through, the duplicate did not: %+v", p.Bearer)
	}
	must(s.SaveCertSet(&model.CertSet{ID: "client", Name: "Client", CA: "ca.pem"}))
	must(s.SaveCertSet(&model.CertSet{ID: "client", Name: "Client", CA: "ca2.pem"}))
	conflictWith(t, s.SaveCertSet(&model.CertSet{ID: "client-2", Name: "Client", CA: "ca.pem"}), "certificate set", "client")
	// an auth profile and a certificate set may share a name: different kinds
	must(s.SaveCertSet(&model.CertSet{ID: "company-token", Name: "Company token", CA: "ca.pem"}))

	// certificate files are replaced only on request
	must(s.SaveCert("ca.pem", strings.NewReader("one"), false))
	if err := s.SaveCert("ca.pem", strings.NewReader("two"), false); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second upload of the same file name: %v", err)
	}
	if read(t, s.Root, "global/certs/ca.pem") != "one" {
		t.Fatal("the refused upload must not change the file")
	}
	must(s.SaveCert("ca.pem", strings.NewReader("two"), true))
	if read(t, s.Root, "global/certs/ca.pem") != "two" {
		t.Fatal("a confirmed replacement must be stored")
	}
}

// Importing something that already exists updates it only when asked to.
func TestImportUpdatesInsteadOfDuplicating(t *testing.T) {
	s := newStore(t)
	sc := scenario()
	if err := s.SaveScenario("shop", sc); err != nil {
		t.Fatal(err)
	}

	// A scenario file with the same name but another id is the same
	// scenario to a person: it updates the existing one and keeps its id.
	incoming := "id: renamed-somewhere-else\nname: " + sc.Name + "\ndescription: from the file\ntests: []\n"
	_, err := s.ImportScenario("shop", []byte(incoming), false)
	conflictWith(t, err, "scenario", sc.ID)
	res, err := s.ImportScenario("shop", []byte(incoming), true)
	if err != nil || res.Scenario != sc.ID {
		t.Fatalf("import as an update: %+v %v", res, err)
	}
	if list, _ := s.ListScenarios("shop"); len(list) != 1 || list[0].Description != "from the file" {
		t.Fatalf("want the one scenario, updated: %+v", list)
	}

	// An application zip: refused while the application exists, unless it
	// is imported as an update.
	var zip bytes.Buffer
	if err := s.ExportApp("shop", &zip, false); err != nil {
		t.Fatal(err)
	}
	_, err = s.ImportApp(zip.Bytes(), false)
	conflictWith(t, err, "application", "shop")
	if _, err := s.ImportApp(zip.Bytes(), true); err != nil {
		t.Fatalf("import as an update: %v", err)
	}
	// The same name under another id would be a second application of
	// that name: refused either way.
	shop, _ := s.GetApp("shop")
	fresh := New(t.TempDir())
	_ = fresh.SaveApp(&model.Application{ID: "storefront", Name: shop.Name})
	for _, replace := range []bool{false, true} {
		_, err = fresh.ImportApp(zip.Bytes(), replace)
		conflictWith(t, err, "application", "storefront")
	}
	if _, err := fresh.GetApp("shop"); err == nil {
		t.Fatal("a refused import must not leave files behind")
	}
}
