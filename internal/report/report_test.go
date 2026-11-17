package report

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/model"
)

func TestJUnit(t *testing.T) {
	r := &model.RunResult{Application: "shop", DurationMs: 1500, Summary: model.Summary{Total: 4, Passed: 1, Failed: 2, Skipped: 1},
		Scenarios: []model.ScenarioResult{{ID: "orders", Name: "Orders <&>", DurationMs: 1500, Tests: []model.TestResult{
			{ID: "a", Name: "Create", Type: "http", Kind: "positive", Status: model.StatusPassed, DurationMs: 250, Properties: map[string]string{"priority": "high"}},
			{ID: "b", Name: "Read", Type: "http", Kind: "positive", Status: model.StatusFailed, Assertions: []model.AssertionResult{{Check: "status", Expected: "200", Actual: "500"}, {Check: "ok", Passed: true}}},
			{ID: "c", Name: "Query", Type: "postgres", Kind: "negative", Status: model.StatusError, Error: "connection refused"},
			{ID: "d", Name: "Later", Type: "http", Kind: "positive", Status: model.StatusSkipped, Error: "earlier step b failed"},
		}}}}
	var buf bytes.Buffer
	if err := JUnit(&buf, r); err != nil {
		t.Fatal(err)
	}
	var doc junitSuites
	if err := xml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid XML: %v\n%s", err, buf.String())
	}
	if doc.Tests != 4 || doc.Failures != 2 || doc.Skipped != 1 || doc.Time != "1.500" {
		t.Fatalf("totals: %+v", doc)
	}
	s := doc.Suites[0]
	if s.Name != "Orders <&>" || s.Tests != 4 || s.Failures != 2 || s.Skipped != 1 {
		t.Fatalf("suite: %+v", s)
	}
	if s.Cases[0].Failure != nil || s.Cases[0].Skipped != nil || s.Cases[0].Name != "[positive] Create" || s.Cases[0].Classname != "shop.orders" {
		t.Errorf("passed case: %+v", s.Cases[0])
	}
	if f := s.Cases[1].Failure; f == nil || f.Message != "status: expected 200, got 500" || strings.Contains(f.Text, "ok") {
		t.Errorf("failed case: %+v", f)
	}
	if f := s.Cases[2].Failure; f == nil || f.Message != "connection refused" {
		t.Errorf("error case: %+v", f)
	}
	if sk := s.Cases[3].Skipped; sk == nil || sk.Message != "earlier step b failed" || s.Cases[3].Failure != nil {
		t.Errorf("skipped case: %+v", s.Cases[3])
	}
	found := false
	for _, p := range s.Cases[0].Props.Items {
		found = found || (p.Name == "priority" && p.Value == "high")
	}
	if !found {
		t.Error("test properties should be exported")
	}
}
