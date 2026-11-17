// Package report renders run results for people and for CI systems.
package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/testplane/testplane/internal/model"
)

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Time     string      `xml:"time,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Props     *junitProps   `xml:"properties,omitempty"`
	Failure   *junitMessage `xml:"failure,omitempty"`
	Skipped   *junitMessage `xml:"skipped,omitempty"`
}

type junitProps struct {
	Items []junitProp `xml:"property"`
}

type junitProp struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitMessage struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

func secs(ms int64) string { return fmt.Sprintf("%.3f", float64(ms)/1000) }

// Failures lists why a test did not pass, one line per reason.
func Failures(t *model.TestResult) []string {
	var out []string
	if t.Error != "" {
		out = append(out, t.Error)
	}
	for _, a := range t.Assertions {
		if !a.Passed {
			out = append(out, fmt.Sprintf("%s: expected %s, got %s", a.Check, a.Expected, a.Actual))
		}
	}
	return out
}

// JUnit writes the run in the JUnit XML format CI servers understand.
func JUnit(w io.Writer, r *model.RunResult) error {
	doc := junitSuites{Name: r.Application, Tests: r.Summary.Total, Failures: r.Summary.Failed, Skipped: r.Summary.Skipped, Time: secs(r.DurationMs)}
	for _, sc := range r.Scenarios {
		s := junitSuite{Name: sc.Name, Time: secs(sc.DurationMs)}
		for i := range sc.Tests {
			t := &sc.Tests[i]
			c := junitCase{Name: fmt.Sprintf("[%s] %s", t.Kind, t.Name), Classname: r.Application + "." + sc.ID, Time: secs(t.DurationMs)}
			if len(t.Properties) > 0 || t.Type != "" {
				c.Props = &junitProps{Items: []junitProp{{"id", t.ID}, {"type", t.Type}, {"kind", t.Kind}}}
				for k, v := range t.Properties {
					c.Props.Items = append(c.Props.Items, junitProp{k, v})
				}
			}
			s.Tests++
			switch t.Status {
			case model.StatusPassed:
			case model.StatusSkipped:
				s.Skipped++
				c.Skipped = &junitMessage{Message: t.Error}
			default:
				s.Failures++
				f := Failures(t)
				msg := "failed"
				if len(f) > 0 {
					msg = f[0]
				}
				c.Failure = &junitMessage{Message: msg, Text: strings.Join(f, "\n")}
			}
			s.Cases = append(s.Cases, c)
		}
		doc.Suites = append(doc.Suites, s)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// JSON writes the full run result.
func JSON(w io.Writer, r *model.RunResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Line prints one test outcome for the console.
func Line(w io.Writer, t *model.TestResult) {
	mark := map[string]string{model.StatusPassed: "PASS", model.StatusFailed: "FAIL", model.StatusError: "ERR ", model.StatusSkipped: "SKIP"}[t.Status]
	indent := "  "
	if t.VariantOf != "" {
		indent = "      "
	}
	fmt.Fprintf(w, "%s%s  %-8s %-8s %s (%d ms)\n", indent, mark, t.Type, t.Kind, t.Name, t.DurationMs)
	if t.Status == model.StatusSkipped {
		return
	}
	for _, f := range Failures(t) {
		fmt.Fprintf(w, "%s        - %s\n", indent, f)
	}
}

// Summary prints the closing totals.
func Summary(w io.Writer, r *model.RunResult) {
	fmt.Fprintf(w, "\n%s: %d passed, %d failed, %d skipped of %d tests in %s s (run %s)\n",
		strings.ToUpper(r.Status), r.Summary.Passed, r.Summary.Failed, r.Summary.Skipped, r.Summary.Total, secs(r.DurationMs), r.ID)
}
