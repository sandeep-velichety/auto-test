package executor

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/model"
)

func TestPgDSNAddsCertificateFiles(t *testing.T) {
	creds := &auth.Credentials{CertFile: "/c/client.crt", KeyFile: "/c/client.key", CAFile: "/c/ca.crt"}
	got, err := pgDSN("postgres://u:p@db:5432/app?application_name=tp", creds)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sslcert=%2Fc%2Fclient.crt", "sslkey=%2Fc%2Fclient.key", "sslrootcert=%2Fc%2Fca.crt", "sslmode=verify-full", "application_name=tp"} {
		if !strings.Contains(got, want) {
			t.Errorf("url dsn %q lacks %s", got, want)
		}
	}
	got, _ = pgDSN("host=db user=u sslmode=require", creds)
	if !strings.Contains(got, "sslcert='/c/client.crt'") || strings.Contains(got, "verify-full") {
		t.Errorf("key=value dsn: %q", got)
	}
	if got, _ = pgDSN("host=db", auth.None()); got != "host=db" {
		t.Errorf("no TLS must leave the dsn alone: %q", got)
	}
}

// Runs against a real server when TESTPLANE_TEST_PG_DSN is set.
func TestPostgresLive(t *testing.T) {
	dsn := os.Getenv("TESTPLANE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TESTPLANE_TEST_PG_DSN not set")
	}
	ctx := context.Background()
	run := func(q string, args ...any) *model.Response {
		t.Helper()
		r, err := Postgres(ctx, &model.PostgresStep{DSN: dsn, Query: q, Args: args}, auth.None())
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return r
	}
	run("DROP TABLE IF EXISTS tp_orders")
	run("CREATE TABLE tp_orders (id int primary key, item text, qty numeric(6,2), paid boolean, created timestamptz default now(), meta jsonb)")
	run("INSERT INTO tp_orders (id, item, qty, paid, meta) VALUES ($1, $2, $3, $4, $5)", 1, "widget", "2.50", true, `{"k":"v"}`)
	r := run("SELECT id, item, qty, paid, created, meta FROM tp_orders WHERE item = $1", "widget")
	if r.Status != 1 || gjson.Get(r.JSON, "count").Int() != 1 {
		t.Fatalf("count: %s", r.JSON)
	}
	row := gjson.Get(r.JSON, "rows.0")
	if row.Get("id").Int() != 1 || row.Get("item").String() != "widget" || row.Get("qty").String() != "2.50" || !row.Get("paid").Bool() || !strings.Contains(row.Get("created").String(), "T") {
		t.Fatalf("row: %s", row.Raw)
	}
	if r = run("SELECT * FROM tp_orders WHERE id = $1", "99"); gjson.Get(r.JSON, "count").Int() != 0 || gjson.Get(r.JSON, "rows.#").Int() != 0 {
		t.Fatalf("empty result: %s", r.JSON)
	}
	if _, err := Postgres(ctx, &model.PostgresStep{DSN: dsn, Query: "SELECT * FROM no_such_table"}, auth.None()); err == nil {
		t.Fatal("a failing query must be an error")
	}
	run("DROP TABLE tp_orders")
}
