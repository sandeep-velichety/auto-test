package model

import "time"

const (
	StatusPassed  = "passed"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
	StatusError   = "error"
)

// Response is the normalised outcome of any step type. JSON is the document
// assertions and captures are evaluated against.
type Response struct {
	Status     int                 `json:"status"`
	StatusText string              `json:"statusText,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       string              `json:"body,omitempty"`
	JSON       string              `json:"-"`
	DurationMs int64               `json:"durationMs"`
}

type AssertionResult struct {
	Check    string `json:"check"`
	Passed   bool   `json:"passed"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
}

type TestResult struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	VariantOf  string            `json:"variantOf,omitempty"`
	Type       string            `json:"type"`
	Kind       string            `json:"kind"`
	Auth       string            `json:"auth,omitempty"`
	MTLS       string            `json:"mtls,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
	Status     string            `json:"status"`
	Attempts   int               `json:"attempts,omitempty"`
	DurationMs int64             `json:"durationMs"`
	Request    any               `json:"request,omitempty"`
	Response   *Response         `json:"response,omitempty"`
	Assertions []AssertionResult `json:"assertions,omitempty"`
	Captured   map[string]string `json:"captured,omitempty"`
	Error      string            `json:"error,omitempty"`
}

type ScenarioResult struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Status     string       `json:"status"`
	DurationMs int64        `json:"durationMs"`
	Tests      []TestResult `json:"tests"`
}

type Summary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

type RunResult struct {
	ID          string           `json:"id"`
	Application string           `json:"application"`
	Environment string           `json:"environment,omitempty"`
	TriggeredBy string           `json:"triggeredBy,omitempty"`
	Source      string           `json:"source,omitempty"` // server | cli
	StartedAt   time.Time        `json:"startedAt"`
	DurationMs  int64            `json:"durationMs"`
	Status      string           `json:"status"`
	Summary     Summary          `json:"summary"`
	Scenarios   []ScenarioResult `json:"scenarios"`
}
