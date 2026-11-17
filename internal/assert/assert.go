// Package assert evaluates path-based checks against a JSON document.
package assert

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/testplane/testplane/internal/model"
)

// Ops lists the supported assertion operators.
var Ops = []string{"equals", "not_equals", "contains", "not_contains", "matches", "exists", "not_exists", "gt", "gte", "lt", "lte", "length", "in"}

// Eval checks one assertion against a JSON document.
func Eval(doc string, a model.Assertion) model.AssertionResult {
	op := a.Op
	if op == "" {
		op = "equals"
		if a.Value == nil {
			op = "exists"
		}
	}
	got := gjson.Get(doc, a.Path)
	res := model.AssertionResult{
		Check:    strings.TrimSpace(fmt.Sprintf("%s %s %s", a.Path, op, show(a.Value))),
		Expected: show(a.Value),
		Actual:   "<missing>",
	}
	if got.Exists() {
		res.Actual = truncate(got.Raw, 500)
	}
	switch op {
	case "exists":
		res.Passed = got.Exists()
	case "not_exists":
		res.Passed = !got.Exists()
	case "equals":
		res.Passed = got.Exists() && equal(got, a.Value)
	case "not_equals":
		res.Passed = !got.Exists() || !equal(got, a.Value)
	case "contains", "not_contains":
		has := false
		if got.IsArray() {
			for _, item := range got.Array() {
				if equal(item, a.Value) {
					has = true
					break
				}
			}
		} else if got.Exists() {
			has = strings.Contains(got.String(), fmt.Sprint(a.Value))
		}
		res.Passed = has == (op == "contains")
	case "matches":
		re, err := regexp.Compile(fmt.Sprint(a.Value))
		if err != nil {
			res.Actual = "invalid regular expression: " + err.Error()
			return res
		}
		res.Passed = got.Exists() && re.MatchString(got.String())
	case "gt", "gte", "lt", "lte":
		want, ok := number(a.Value)
		have, ok2 := number(got.Value())
		if !ok || !ok2 || !got.Exists() {
			return res
		}
		switch op {
		case "gt":
			res.Passed = have > want
		case "gte":
			res.Passed = have >= want
		case "lt":
			res.Passed = have < want
		case "lte":
			res.Passed = have <= want
		}
	case "length":
		want, ok := number(a.Value)
		if !ok || !got.Exists() {
			return res
		}
		n := 0
		switch {
		case got.IsArray():
			n = len(got.Array())
		case got.IsObject():
			n = len(got.Map())
		default:
			n = len([]rune(got.String()))
		}
		res.Actual = strconv.Itoa(n)
		res.Passed = float64(n) == want
	case "in":
		list, ok := a.Value.([]any)
		if !ok {
			res.Actual = "'in' needs a list value"
			return res
		}
		for _, v := range list {
			if got.Exists() && equal(got, v) {
				res.Passed = true
				break
			}
		}
	default:
		res.Actual = "unknown operator " + op
	}
	return res
}

// equal compares loosely on purpose: templated values are always strings,
// so "42" must match the number 42.
func equal(got gjson.Result, want any) bool {
	switch w := want.(type) {
	case nil:
		return got.Type == gjson.Null
	case string:
		return got.String() == w
	case bool:
		return (got.Type == gjson.True || got.Type == gjson.False) && got.Bool() == w
	}
	if n, ok := number(want); ok {
		g, ok2 := number(got.Value())
		return ok2 && g == n
	}
	var a, b any
	wb, err := json.Marshal(want)
	if err != nil || json.Unmarshal(wb, &a) != nil || json.Unmarshal([]byte(got.Raw), &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	}
	return 0, false
}

func show(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
