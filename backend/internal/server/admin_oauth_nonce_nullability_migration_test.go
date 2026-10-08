package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestAdminOAuthNonceNullabilityMigrationPreservesLegacyRows(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "nonce.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE admin_o_auth_oidc_nonces (flow_id text PRIMARY KEY, nonce text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO admin_o_auth_oidc_nonces (flow_id, nonce) VALUES ('flow-1', 'nonce-1')`); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := normalizeAdminOAuthNonceNullability(context.Background(), directSQLMigrationExecer{DB: db}); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt+1, err)
		}
	}
	var notNull int
	if err := db.QueryRow(`SELECT "notnull" FROM pragma_table_info('admin_o_auth_oidc_nonces') WHERE name = 'nonce'`).Scan(&notNull); err != nil {
		t.Fatal(err)
	}
	if notNull != 0 {
		t.Fatalf("nonce notnull = %d, want 0", notNull)
	}
	var nonce string
	if err := db.QueryRow(`SELECT nonce FROM admin_o_auth_oidc_nonces WHERE flow_id = 'flow-1'`).Scan(&nonce); err != nil {
		t.Fatal(err)
	}
	if nonce != "nonce-1" {
		t.Fatalf("preserved nonce = %q, want nonce-1", nonce)
	}
}
