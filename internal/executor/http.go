// Package executor runs one step of each supported type and normalises the
// outcome into a model.Response.
package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/model"
)

const maxBody = 5 << 20

func timeout(ms, def int) time.Duration {
	if ms <= 0 {
		ms = def
	}
	return time.Duration(ms) * time.Millisecond
}

// asJSON returns body when it already is JSON, otherwise body as a JSON string.
func asJSON(body []byte) string {
	if len(bytes.TrimSpace(body)) > 0 && json.Valid(body) {
		return string(body)
	}
	b, _ := json.Marshal(string(body))
	return string(b)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// HTTP performs one HTTP call. Headers set on the step win over headers
// from the auth profile; a header set to "" is removed altogether.
func HTTP(ctx context.Context, s *model.HTTPStep, creds *auth.Credentials) (*model.Response, error) {
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid url %q (is a variable missing from the environment?)", s.URL)
	}
	q := u.Query()
	for k, v := range creds.Query {
		q.Set(k, v)
	}
	for k, v := range s.Query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	var body io.Reader
	contentType := ""
	switch {
	case len(s.Form) > 0:
		f := url.Values{}
		for k, v := range s.Form {
			f.Set(k, v)
		}
		body, contentType = strings.NewReader(f.Encode()), "application/x-www-form-urlencoded"
	case s.Body != nil:
		if str, ok := s.Body.(string); ok {
			body = strings.NewReader(str)
			if json.Valid([]byte(str)) {
				contentType = "application/json"
			}
		} else {
			b, err := json.Marshal(s.Body)
			if err != nil {
				return nil, fmt.Errorf("encode body: %w", err)
			}
			body, contentType = bytes.NewReader(b), "application/json"
		}
	}
	method := strings.ToUpper(s.Method)
	if method == "" {
		method = http.MethodGet
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(s.TimeoutMs, 30000))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range creds.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range s.Headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if creds.TLS != nil {
		tr.TLSClientConfig = creds.TLS.Clone()
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	if !s.FollowRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return &model.Response{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    resp.Header,
		Body:       string(b),
		JSON:       asJSON(b),
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}
