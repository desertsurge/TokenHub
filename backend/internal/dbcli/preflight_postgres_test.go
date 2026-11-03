//go:build integration

package dbcli

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/dbschema"
)

func TestPreflightPostgresReadOnly(t *testing.T) {
	rawURL := os.Getenv("TEST_POSTGRES_URL")
	if rawURL == "" {
		t.Skip("TEST_POSTGRES_URL not set")
	}
	admin, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("preflight_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	})
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	setup, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = setup.Close() }()
	query.Set("default_transaction_read_only", "on")
	parsed.RawQuery = query.Encode()
	t.Setenv("TOKENHUB_DATABASE_URL", parsed.String())
	code, output := runCLI(t, "preflight")
	if code != 1 || !strings.Contains(output, "baseline") {
		t.Fatalf("missing baseline: code=%d output=%q", code, output)
	}
	var count int
	if err := setup.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_schema = $1", schema).Scan(&count); err != nil || count != 0 {
		t.Fatalf("preflight initialized tables: count=%d err=%v", count, err)
	}
	runner, err := dbschema.NewRunner(setup, dbschema.DialectPostgres, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Adopt(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	code, output = runCLI(t, "preflight")
	if code != 0 || !strings.Contains(output, "baseline recorded:   true") ||
		!strings.Contains(output, "pending expand:") || strings.Contains(output, "data backfills:") {
		t.Fatalf("read-only preflight: code=%d output=%q", code, output)
	}
	if err := setup.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'data_backfills'", schema).Scan(&count); err != nil || count != 0 {
		t.Fatalf("preflight initialized backfills: count=%d err=%v", count, err)
	}
	for _, tc := range []struct{ sql, message string }{
		{"UPDATE schema_migrations SET dirty = 1", "dirty"},
		{"UPDATE schema_migrations SET dirty = 0, checksum = 'drift'", "checksum"},
	} {
		if _, err := setup.Exec(tc.sql); err != nil {
			t.Fatal(err)
		}
		code, output = runCLI(t, "preflight")
		if code != 1 || !strings.Contains(output, tc.message) {
			t.Fatalf("invalid ledger: code=%d output=%q", code, output)
		}
	}
	if _, err := setup.Exec("UPDATE schema_migrations SET checksum = $1", dbschema.AdoptionChecksum); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec("INSERT INTO schema_migrations (version,name,phase,checksum) VALUES (999,'unknown','expand','x')"); err != nil {
		t.Fatal(err)
	}
	code, output = runCLI(t, "preflight")
	if code != 1 || !strings.Contains(output, "unknown") {
		t.Fatalf("unknown version: code=%d output=%q", code, output)
	}
}
