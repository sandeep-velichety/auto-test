package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/model"
)

// Logs searches a log platform. Only Datadog (Logs Search API v2) is
// supported. The result document is {"count": n, "logs": [{timestamp,
// status, service, host, message, tags, attributes}]}, oldest first.
//
// Keys come from the step (apiKey, appKey) or from headers supplied by the
// auth profile (DD-API-KEY, DD-APPLICATION-KEY).
func Logs(ctx context.Context, s *model.LogsStep, creds *auth.Credentials, scenarioStart time.Time) (*model.Response, error) {
	if p := strings.ToLower(s.Provider); p != "" && p != "datadog" {
		return nil, fmt.Errorf("logs.provider %q is not supported (only datadog)", s.Provider)
	}
	base := strings.TrimRight(s.URL, "/")
	if base == "" {
		site := s.Site
		if site == "" {
			site = "datadoghq.com"
		}
		base = "https://api." + site
	}
	from := s.From
	if from == "" || strings.EqualFold(from, "scenario_start") {
		// A few seconds of slack for clock differences between machines.
		from = scenarioStart.Add(-5 * time.Second).UTC().Format(time.RFC3339)
	}
	to := s.To
	if to == "" {
		to = "now"
	}
	limit := s.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	body, _ := json.Marshal(map[string]any{
		"filter": map[string]any{"query": s.Query, "from": from, "to": to},
		"sort":   "timestamp",
		"page":   map[string]any{"limit": limit},
	})
	ctx, cancel := context.WithTimeout(ctx, timeout(s.TimeoutMs, 30000))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v2/logs/events/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("logs.url: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range creds.Headers {
		req.Header.Set(k, v)
	}
	if s.APIKey != "" {
		req.Header.Set("DD-API-KEY", s.APIKey)
	}
	if s.AppKey != "" {
		req.Header.Set("DD-APPLICATION-KEY", s.AppKey)
	}
	if req.Header.Get("DD-API-KEY") == "" || req.Header.Get("DD-APPLICATION-KEY") == "" {
		return nil, fmt.Errorf("datadog needs an API key and an application key: set logs.apiKey and logs.appKey (for example {{$env.DD_API_KEY}} and {{$env.DD_APP_KEY}})")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if creds.TLS != nil {
		tr.TLSClientConfig = creds.TLS.Clone()
	}
	defer tr.CloseIdleConnections()
	start := time.Now()
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("log search: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("log search: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := string(raw)
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return nil, fmt.Errorf("log search answered %d: %s", resp.StatusCode, msg)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("log search did not return JSON")
	}
	logs := []map[string]any{}
	for _, item := range gjson.GetBytes(raw, "data").Array() {
		a := item.Get("attributes")
		entry := map[string]any{
			"id":        item.Get("id").String(),
			"timestamp": a.Get("timestamp").String(),
			"status":    a.Get("status").String(),
			"service":   a.Get("service").String(),
			"host":      a.Get("host").String(),
			"message":   a.Get("message").String(),
		}
		if v := a.Get("tags"); v.Exists() {
			entry["tags"] = json.RawMessage(v.Raw)
		}
		if v := a.Get("attributes"); v.Exists() {
			entry["attributes"] = json.RawMessage(v.Raw)
		}
		logs = append(logs, entry)
	}
	doc := mustJSON(map[string]any{"count": len(logs), "logs": logs})
	return &model.Response{Status: len(logs), StatusText: fmt.Sprintf("%d log entr%s", len(logs), map[bool]string{true: "y", false: "ies"}[len(logs) == 1]),
		Body: doc, JSON: doc, DurationMs: time.Since(start).Milliseconds()}, nil
}
