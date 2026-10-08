package server

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"tokenhub/backend/internal/dbschema"
)

func TestSQLiteGatewayExpandMigrationUpgradesV6Schema(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE api_keys (id text PRIMARY KEY, project_id text, owner_user_id text, name text, key_hash text, status text)`,
		`CREATE TABLE usage_records (id text PRIMARY KEY, request_id text, project_id text, api_key_id text, created_at datetime)`,
		`CREATE TABLE gateway_tenants (id text PRIMARY KEY, external_tenant_id text, name text, status text, version integer, synced_at datetime, deleted_at datetime, created_at datetime, updated_at datetime)`,
		`INSERT INTO api_keys (id, name, key_hash, status) VALUES ('key_existing', 'Existing key', 'hash_existing', 'active')`,
		`INSERT INTO gateway_tenants (id, external_tenant_id, name, status, version) VALUES ('tenant_existing', 'tenant_existing', 'Existing tenant', 'active', 4)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	migration := gatewayControlPlaneRepairMigration()
	if migration.Go == nil {
		t.Fatal("SQLite Gateway schema migration is not registered as a Go migration")
	}
	if err := migration.Go(context.Background(), directSQLMigrationExecer{DB: db}); err != nil {
		t.Fatal(err)
	}

	var keyName string
	if err := db.QueryRow("SELECT name FROM api_keys WHERE id = 'key_existing'").Scan(&keyName); err != nil {
		t.Fatal(err)
	}
	if keyName != "Existing key" {
		t.Fatalf("existing API key was not preserved: %q", keyName)
	}
	var tenantName string
	if err := db.QueryRow("SELECT name FROM gateway_tenants WHERE id = 'tenant_existing'").Scan(&tenantName); err != nil {
		t.Fatal(err)
	}
	if tenantName != "Existing tenant" {
		t.Fatalf("existing Gateway tenant was not preserved: %q", tenantName)
	}
	for _, table := range []string{"gateway_tenants", "gateway_organizations", "gateway_principals", "gateway_principal_organization_bindings", "gateway_projects", "gateway_workloads", "integration_inbox"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("expected migrated table %s, count=%d err=%v", table, count, err)
		}
	}
	var usageIndexCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_usage_request_key'").Scan(&usageIndexCount); err != nil || usageIndexCount != 1 {
		t.Fatalf("expected migrated usage request index, count=%d err=%v", usageIndexCount, err)
	}
	rows, err := db.Query("PRAGMA table_info(api_keys)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns := make([]string, 0)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	for _, column := range []string{"tenant_external_id", "project_external_id", "principal_type", "principal_external_id", "environment", "managed_by", "control_request_id", "control_request_digest", "key_ciphertext"} {
		if !containsGatewayMigrationColumn(columns, column) {
			t.Fatalf("expected API key column %s, columns=%s", column, strings.Join(columns, ","))
		}
	}
}

func TestGatewaySchemaHistoryRemainsForwardCompatible(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	baseline, err := dbschema.SQLiteBaselineStatements()
	if err != nil {
		t.Fatal(err)
	}
	registry := SchemaMigrationRegistry()
	historical := make([]dbschema.Migration, 0, len(registry)-1)
	for _, migration := range registry {
		if migration.Version < 15 {
			historical = append(historical, migration)
		}
	}
	historicalRunner, err := dbschema.NewRunner(db, dbschema.DialectSQLite, historical, dbschema.WithFreshBaseline(baseline))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := historicalRunner.Adopt(context.Background(), nil); err != nil {
		t.Fatalf("apply historical registry: %v", err)
	}
	for version, checksum := range map[int64]string{
		7: "tokenhub-schema-gateway-integration-v1",
		8: "tokenhub-schema-gateway-integration-storage-v1",
	} {
		var recorded string
		if err := db.QueryRow("SELECT checksum FROM schema_migrations WHERE version = ?", version).Scan(&recorded); err != nil {
			t.Fatal(err)
		}
		if recorded != checksum {
			t.Fatalf("historical migration %d checksum = %q, want %q", version, recorded, checksum)
		}
	}
	if _, err := db.Exec("DROP INDEX IF EXISTS idx_usage_records_project_created"); err != nil {
		t.Fatal(err)
	}

	currentRunner, err := dbschema.NewRunner(db, dbschema.DialectSQLite, registry)
	if err != nil {
		t.Fatal(err)
	}
	result, err := currentRunner.Migrate(context.Background())
	if err != nil {
		t.Fatalf("upgrade historical ledger: %v", err)
	}
	if len(result.Applied) != 4 || result.Applied[0].Version != 15 || result.Applied[1].Version != 16 || result.Applied[2].Version != 17 || result.Applied[3].Version != 18 {
		t.Fatalf("applied migrations = %+v, want versions 15 through 18", result.Applied)
	}
	var indexCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_usage_records_project_created'").Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatalf("Gateway schema repair index count = %d, want 1", indexCount)
	}
	var emailColumnCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('gateway_principals') WHERE name = 'email'").Scan(&emailColumnCount); err != nil {
		t.Fatal(err)
	}
	if emailColumnCount != 1 {
		t.Fatalf("Gateway principal email column count = %d, want 1", emailColumnCount)
	}
}

func containsGatewayMigrationColumn(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
