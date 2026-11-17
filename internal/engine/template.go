// Package engine runs scenarios: it resolves variables and auth, executes
// tests in order, evaluates expectations and collects results.
package engine

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var tmplRe = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

func uuid4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// renderer substitutes {{name}} placeholders and remembers the names it
// could not resolve.
type renderer struct {
	vars    map[string]string
	missing map[string]bool
}

func newRenderer(vars map[string]string) *renderer {
	return &renderer{vars: vars, missing: map[string]bool{}}
}

func (r *renderer) lookup(name string) (string, bool) {
	switch {
	case name == "$uuid":
		return uuid4(), true
	case name == "$timestamp":
		return strconv.FormatInt(time.Now().Unix(), 10), true
	case name == "$timestampMs":
		return strconv.FormatInt(time.Now().UnixMilli(), 10), true
	case name == "$isoTimestamp":
		return time.Now().UTC().Format(time.RFC3339), true
	case name == "$randomInt":
		n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
		return n.String(), true
	case strings.HasPrefix(name, "$env."):
		return os.LookupEnv(strings.TrimPrefix(name, "$env."))
	}
	v, ok := r.vars[name]
	return v, ok
}

func (r *renderer) str(s string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return tmplRe.ReplaceAllStringFunc(s, func(m string) string {
		name := tmplRe.FindStringSubmatch(m)[1]
		if v, ok := r.lookup(name); ok {
			return v
		}
		r.missing[name] = true
		return m
	})
}

// value renders every string inside a decoded JSON/YAML value.
func (r *renderer) value(v any) any {
	switch x := v.(type) {
	case string:
		return r.str(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[r.str(k)] = r.value(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = r.value(val)
		}
		return out
	}
	return v
}

func (r *renderer) err() error {
	if len(r.missing) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.missing))
	for n := range r.missing {
		names = append(names, "{{"+n+"}}")
	}
	sort.Strings(names)
	return fmt.Errorf("undefined variable(s): %s", strings.Join(names, ", "))
}

// toMap converts any struct or map to a generic map through JSON.
func toMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if string(b) == "null" {
		return m, nil
	}
	return m, json.Unmarshal(b, &m)
}

func fromMap(m any, out any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// merge lays override on top of base. Maps merge key by key, everything
// else is replaced, and a null removes the key.
func merge(base, override map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		if v == nil {
			delete(out, k)
			continue
		}
		bm, ok1 := out[k].(map[string]any)
		om, ok2 := v.(map[string]any)
		if ok1 && ok2 {
			out[k] = merge(bm, om)
		} else {
			out[k] = v
		}
	}
	return out
}
