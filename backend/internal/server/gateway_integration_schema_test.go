package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestGatewayIntegrationMigrationIsRegistered(t *testing.T) {
	registered := map[int64]string{}
	for _, migration := range SchemaMigrationRegistry() {
		registered[migration.Version] = migration.Name
	}
	for version, name := range map[int64]string{
		7: "add-gateway-integration-schema",
		8: "complete-gateway-integration-schema",
	} {
		if registered[version] != name {
			t.Errorf("migration version %d = %q, want %q", version, registered[version], name)
		}
	}
}

func TestGatewayIntegrationMigrationUpgradesLegacySQLiteSchema(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "legacy-gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`CREATE TABLE api_keys (id text PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	migration := gatewayIntegrationMigration()
	for attempt := 0; attempt < 2; attempt++ {
		if err := migration.Go(context.Background(), directSQLMigrationExecer{DB: database}); err != nil {
			t.Fatalf("apply gateway migration attempt %d: %v", attempt+1, err)
		}
	}

	for _, column := range gatewayAPIKeyColumns {
		var count int
		if err := database.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('api_keys') WHERE name = ?`, column,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("api_keys.%s count = %d, want 1", column, count)
		}
	}
	for table := range gatewayTableColumns {
		var count int
		if err := database.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("table %s count = %d, want 1", table, count)
		}
	}
}

func TestGatewayIntegrationStorageMigrationUpgradesLegacySQLiteSchema(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "legacy-gateway-storage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, statement := range []string{
		`CREATE TABLE api_keys (id text PRIMARY KEY)`,
		`CREATE TABLE usage_records (id text PRIMARY KEY, request_id text, api_key_id text)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	migration := gatewayIntegrationStorageMigration()
	for attempt := 0; attempt < 2; attempt++ {
		if err := migration.Go(context.Background(), directSQLMigrationExecer{DB: database}); err != nil {
			t.Fatalf("apply gateway storage migration attempt %d: %v", attempt+1, err)
		}
	}

	var count int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('api_keys') WHERE name = 'key_ciphertext'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("api_keys.key_ciphertext count = %d, want 1", count)
	}
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_usage_request_key'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("idx_usage_request_key count = %d, want 1", count)
	}
}
