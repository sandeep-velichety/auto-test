package executor

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/model"
)

// pgDSN adds the profile's certificate files to a DSN in either URL or
// key=value form.
func pgDSN(dsn string, creds *auth.Credentials) (string, error) {
	extra := map[string]string{}
	if creds.CertFile != "" {
		extra["sslcert"], extra["sslkey"] = creds.CertFile, creds.KeyFile
	}
	if creds.CAFile != "" {
		extra["sslrootcert"] = creds.CAFile
	}
	if len(extra) == 0 {
		return dsn, nil
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", fmt.Errorf("postgres.dsn: %w", err)
		}
		q := u.Query()
		for k, v := range extra {
			q.Set(k, v)
		}
		if q.Get("sslmode") == "" {
			q.Set("sslmode", "verify-full")
		}
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	for k, v := range extra {
		dsn += fmt.Sprintf(" %s='%s'", k, strings.ReplaceAll(v, "'", `\'`))
	}
	if !strings.Contains(dsn, "sslmode=") {
		dsn += " sslmode=verify-full"
	}
	return dsn, nil
}

// Postgres runs one statement. The result document is
// {"count": n, "rows": [{column: value}]}.
func Postgres(ctx context.Context, s *model.PostgresStep, creds *auth.Credentials) (*model.Response, error) {
	dsn, err := pgDSN(s.DSN, creds)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(s.TimeoutMs, 15000))
	defer cancel()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres.dsn: %w", err)
	}
	defer db.Close()
	start := time.Now()
	rows, err := db.QueryContext(ctx, s.Query, s.Args...)
	if err != nil {
		return nil, fmt.Errorf("postgres query: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("postgres scan: %w", err)
		}
		row := map[string]any{}
		for i, c := range cols {
			switch v := vals[i].(type) {
			case []byte:
				row[c] = string(v)
			case time.Time:
				row[c] = v.UTC().Format(time.RFC3339Nano)
			default:
				row[c] = v
			}
		}
		out = append(out, row)
		if len(out) >= 1000 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres query: %w", err)
	}
	doc := mustJSON(map[string]any{"count": len(out), "rows": out})
	return &model.Response{Status: len(out), StatusText: fmt.Sprintf("%d row(s)", len(out)), Body: doc, JSON: doc, DurationMs: time.Since(start).Milliseconds()}, nil
}
