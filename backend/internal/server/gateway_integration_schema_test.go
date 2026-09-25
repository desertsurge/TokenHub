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
		7:  "add-gateway-integration-schema",
		8:  "complete-gateway-integration-schema",
		9:  "add-gateway-managed-users",
		10: "add-admin-oauth-oidc-nonce",
		11: "add-gateway-cost-center-projections",
		12: "add-gateway-organization-primary",
	} {
		if registered[version] != name {
			t.Errorf("migration version %d = %q, want %q", version, registered[version], name)
		}
	}
}

func TestGatewayOrganizationPrimaryMigrationUpgradesLegacySQLiteSchema(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "legacy-organization-primary.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`CREATE TABLE gateway_principal_organization_bindings (id text PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := gatewayOrganizationPrimaryMigration().Go(context.Background(), directSQLMigrationExecer{DB: database}); err != nil {
			t.Fatalf("apply organization primary migration attempt %d: %v", attempt+1, err)
		}
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('gateway_principal_organization_bindings') WHERE name = 'is_primary' AND "notnull" = 1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("gateway organization primary column count = %d, want 1", count)
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

func TestAdminOAuthNonceMigrationCreatesExtensionTable(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "oauth-nonce.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	migration := adminOAuthNonceMigration()
	for attempt := 0; attempt < 2; attempt++ {
		if err := migration.Go(context.Background(), directSQLMigrationExecer{DB: database}); err != nil {
			t.Fatalf("apply oauth nonce migration attempt %d: %v", attempt+1, err)
		}
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'admin_o_auth_oidc_nonces'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("oauth nonce extension table count = %d, want 1", count)
	}
}

func TestGatewayCostCenterMigrationUpgradesLegacySQLiteSchema(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "legacy-cost-center.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`CREATE TABLE gateway_projects (id text PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	migration := gatewayCostCenterMigration()
	for attempt := 0; attempt < 2; attempt++ {
		if err := migration.Go(context.Background(), directSQLMigrationExecer{DB: database}); err != nil {
			t.Fatalf("apply cost center migration attempt %d: %v", attempt+1, err)
		}
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('gateway_projects') WHERE name = 'cost_center_id'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("gateway_projects.cost_center_id count = %d, want 1", count)
	}
	for _, table := range []string{"gateway_cost_centers", "gateway_organization_cost_centers"} {
		if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s count = %d, want 1", table, count)
		}
	}
}

func TestUsageCostCenterSnapshotMigrationUpgradesLegacySQLiteSchema(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "legacy-usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`CREATE TABLE usage_records (id text PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	migration := usageCostCenterSnapshotMigration()
	for attempt := 0; attempt < 2; attempt++ {
		if err := migration.Go(context.Background(), directSQLMigrationExecer{DB: database}); err != nil {
			t.Fatalf("apply usage cost center migration attempt %d: %v", attempt+1, err)
		}
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('usage_records') WHERE name = 'cost_center_snapshot'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("usage_records.cost_center_snapshot count = %d, want 1", count)
	}
}
