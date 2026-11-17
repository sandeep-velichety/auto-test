package engine

import (
	"reflect"
	"regexp"
	"testing"
)

func TestRender(t *testing.T) {
	t.Setenv("TP_TEST_SECRET", "s3cret")
	r := newRenderer(map[string]string{"baseUrl": "http://x", "id": "7"})
	if got := r.str("{{baseUrl}}/orders/{{ id }}"); got != "http://x/orders/7" {
		t.Fatalf("got %q", got)
	}
	if got := r.str("{{$env.TP_TEST_SECRET}}"); got != "s3cret" {
		t.Fatalf("env: got %q", got)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(r.str("{{$uuid}}")) {
		t.Fatal("uuid is not a v4 uuid")
	}
	if err := r.err(); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	got := r.value(map[string]any{"a": []any{"{{id}}", 3, true}, "b": map[string]any{"c": "{{nope}}"}})
	want := map[string]any{"a": []any{"7", 3, true}, "b": map[string]any{"c": "{{nope}}"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	if err := r.err(); err == nil || err.Error() != "undefined variable(s): {{nope}}" {
		t.Fatalf("missing variable not reported: %v", err)
	}
}

func TestMerge(t *testing.T) {
	base := map[string]any{"method": "POST", "headers": map[string]any{"A": "1", "B": "2"}, "body": map[string]any{"item": "x", "quantity": 2}}
	over := map[string]any{"headers": map[string]any{"B": "", "C": "3"}, "body": map[string]any{"quantity": 0}, "method": nil}
	got := merge(base, over)
	want := map[string]any{"headers": map[string]any{"A": "1", "B": "", "C": "3"}, "body": map[string]any{"item": "x", "quantity": 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	if base["method"] != "POST" || base["body"].(map[string]any)["quantity"] != 2 {
		t.Fatal("merge changed its base")
	}
}
