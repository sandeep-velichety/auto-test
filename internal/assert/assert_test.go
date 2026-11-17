package assert

import (
	"testing"

	"github.com/testplane/testplane/internal/model"
)

func TestEval(t *testing.T) {
	doc := `{"id":42,"name":"widget","tags":["a","b"],"ok":true,"nothing":null,"nested":{"n":3.5},"rows":[{"x":1},{"x":2}]}`
	cases := []struct {
		name string
		a    model.Assertion
		want bool
	}{
		{"number equals number", model.Assertion{Path: "id", Value: 42}, true},
		{"number equals templated string", model.Assertion{Path: "id", Value: "42"}, true},
		{"number mismatch", model.Assertion{Path: "id", Value: 43}, false},
		{"string equals", model.Assertion{Path: "name", Value: "widget"}, true},
		{"bool equals", model.Assertion{Path: "ok", Value: true}, true},
		{"bool does not equal string field", model.Assertion{Path: "name", Value: true}, false},
		{"missing path fails equals", model.Assertion{Path: "absent", Value: "x"}, false},
		{"no value means exists", model.Assertion{Path: "name"}, true},
		{"exists on missing", model.Assertion{Path: "absent", Op: "exists"}, false},
		{"not_exists", model.Assertion{Path: "absent", Op: "not_exists"}, true},
		{"not_exists on present", model.Assertion{Path: "id", Op: "not_exists"}, false},
		{"not_equals", model.Assertion{Path: "name", Op: "not_equals", Value: "gadget"}, true},
		{"contains substring", model.Assertion{Path: "name", Op: "contains", Value: "idg"}, true},
		{"contains array item", model.Assertion{Path: "tags", Op: "contains", Value: "b"}, true},
		{"contains array item missing", model.Assertion{Path: "tags", Op: "contains", Value: "z"}, false},
		{"not_contains", model.Assertion{Path: "tags", Op: "not_contains", Value: "z"}, true},
		{"matches", model.Assertion{Path: "name", Op: "matches", Value: "^wid"}, true},
		{"bad regex fails", model.Assertion{Path: "name", Op: "matches", Value: "("}, false},
		{"gt", model.Assertion{Path: "nested.n", Op: "gt", Value: 3}, true},
		{"gte boundary", model.Assertion{Path: "nested.n", Op: "gte", Value: 3.5}, true},
		{"lt false", model.Assertion{Path: "id", Op: "lt", Value: 42}, false},
		{"lte with string number", model.Assertion{Path: "id", Op: "lte", Value: "42"}, true},
		{"length of array", model.Assertion{Path: "rows", Op: "length", Value: 2}, true},
		{"length of string", model.Assertion{Path: "name", Op: "length", Value: 6}, true},
		{"array count path", model.Assertion{Path: "rows.#", Value: 2}, true},
		{"nested array path", model.Assertion{Path: "rows.1.x", Value: 2}, true},
		{"in list", model.Assertion{Path: "name", Op: "in", Value: []any{"gadget", "widget"}}, true},
		{"in list miss", model.Assertion{Path: "name", Op: "in", Value: []any{"gadget"}}, false},
		{"object equals", model.Assertion{Path: "nested", Op: "equals", Value: map[string]any{"n": 3.5}}, true},
		{"unknown operator fails", model.Assertion{Path: "id", Op: "roughly", Value: 42}, false},
	}
	for _, c := range cases {
		if got := Eval(doc, c.a); got.Passed != c.want {
			t.Errorf("%s: passed=%v, want %v (%+v)", c.name, got.Passed, c.want, got)
		}
	}
}
