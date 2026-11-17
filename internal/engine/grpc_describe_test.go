package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/testplane/testplane/internal/model"
	"github.com/testplane/testplane/internal/store"
)

func TestDescribeGRPCAndRunUntil(t *testing.T) {
	e, st := fixture(t)
	ctx := context.Background()
	svcs, err := e.DescribeGRPC(ctx, "shop", "", "none", "", model.GRPCStep{Target: "{{grpcTarget}}", Plaintext: true})
	must(t, err)
	if len(svcs) != 1 || svcs[0].Name != "grpc.health.v1.Health" {
		t.Fatalf("services (reflection itself is hidden): %+v", svcs)
	}
	byName := map[string]int{}
	for i, m := range svcs[0].Methods {
		byName[m.Name] = i
	}
	check, watch := svcs[0].Methods[byName["Check"]], svcs[0].Methods[byName["Watch"]]
	if check.FullName != "grpc.health.v1.Health/Check" || check.InputType != "grpc.health.v1.HealthCheckRequest" || check.ServerStreaming || !watch.ServerStreaming {
		t.Fatalf("methods: %+v / %+v", check, watch)
	}
	tpl, _ := json.Marshal(check.Template)
	if string(tpl) != `{"service":""}` {
		t.Fatalf("request template should list every field: %s", tpl)
	}
	for name, step := range map[string]model.GRPCStep{
		"undefined variable": {Target: "{{nope}}", Plaintext: true},
		"empty target":       {Plaintext: true},
		"nothing listening":  {Target: "127.0.0.1:1", Plaintext: true, TimeoutMs: 2000},
		"tls to plaintext":   {Target: "{{grpcTarget}}", TimeoutMs: 3000},
	} {
		if _, err := e.DescribeGRPC(ctx, "shop", "", "none", "", step); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: want a clear error, got %v", name, err)
		}
	}
	if _, err := e.DescribeGRPC(ctx, "shop", "ghost", "none", "", model.GRPCStep{Target: "x:1"}); err == nil {
		t.Error("unknown environment must fail")
	}

	// Run to a step: everything up to and including it (with its cases), nothing after.
	save(t, st, lifecycle())
	res, err := e.Run(ctx, Options{App: "shop", Until: "read"})
	must(t, err)
	var ids []string
	for _, tr := range res.Scenarios[0].Tests {
		ids = append(ids, tr.ID)
	}
	if got := strings.Join(ids, " "); got != "create create.no-token create.stale-token create.zero create.also-ok read" {
		t.Fatalf("until read: %s", got)
	}
	if res.Status != model.StatusPassed || res.Summary.Total != 6 {
		t.Fatalf("summary: %+v", res.Summary)
	}

	// A list message sends several requests on a client stream; a server
	// stream returns its messages under "responses" (here one, then the
	// deadline ends the watch).
	save(t, st, &model.Scenario{ID: "stream", Auth: "none", Tests: []model.Test{
		{ID: "watch", Type: model.TypeGRPC, GRPC: &model.GRPCStep{Target: "{{grpcTarget}}", Method: "grpc.health.v1.Health/Watch", Plaintext: true, Message: map[string]any{"service": "orders"}, TimeoutMs: 1500},
			Expect: &model.Expect{Code: "DEADLINE_EXCEEDED"}},
	}})
	res, err = e.Run(ctx, Options{App: "shop", Scenarios: []string{"stream"}})
	must(t, err)
	if w := find(t, res, "watch"); w.Status != model.StatusPassed {
		t.Errorf("server stream that ends on the deadline: %+v", w)
	}
}
