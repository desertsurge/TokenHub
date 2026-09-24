package server

import (
	"context"
	"fmt"

	"tokenhub/backend/internal/dbschema"
)

// gatewayCostCenterMigration expands already-adopted gateway databases with
// the cost-center projection tables and the project association column.
func gatewayCostCenterMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          11,
		Name:             "add-gateway-cost-center-projections",
		Go:               addGatewayCostCenterSchema,
		ChecksumOverride: "tokenhub-schema-gateway-cost-center-v1",
		StatementBudget:  40,
	}
}

func addGatewayCostCenterSchema(ctx context.Context, db dbschema.MigrationExecer) error {
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err == nil {
		return addGatewayCostCenterSchemaPostgres(ctx, db)
	}
	return addGatewayCostCenterSchemaSQLite(ctx, db)
}

func addGatewayCostCenterSchemaPostgres(ctx context.Context, db dbschema.MigrationExecer) error {
	if _, err := db.ExecContext(ctx, `ALTER TABLE gateway_projects ADD COLUMN IF NOT EXISTS cost_center_id text`); err != nil {
		return fmt.Errorf("add gateway project cost center column: %w", err)
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS gateway_cost_centers (
			id text PRIMARY KEY, tenant_id text, external_cost_center_id text,
			code text, name text, status text, version bigint, synced_at timestamptz,
			deleted_at timestamptz, created_at timestamptz, updated_at timestamptz
		)`,
		`CREATE TABLE IF NOT EXISTS gateway_organization_cost_centers (
			id text PRIMARY KEY, tenant_id text, external_binding_id text,
			organization_id text, cost_center_id text, status text, version bigint,
			synced_at timestamptz, deleted_at timestamptz, created_at timestamptz, updated_at timestamptz
		)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create gateway cost center projection table: %w", err)
		}
	}
	for _, index := range gatewayCostCenterIndexes {
		if err := createSchemaIndex(ctx, db, index.table, index.name, index.column, index.unique); err != nil {
			return err
		}
	}
	return createSchemaIndex(ctx, db, "gateway_projects", "idx_gateway_projects_cost_center_id", "cost_center_id", false)
}

func addGatewayCostCenterSchemaSQLite(ctx context.Context, db dbschema.MigrationExecer) error {
	exists, err := sqliteColumnExists(ctx, db, "gateway_projects", "cost_center_id")
	if err != nil {
		return fmt.Errorf("inspect gateway project cost center column: %w", err)
	}
	if !exists {
		if _, err := db.ExecContext(ctx, `ALTER TABLE "gateway_projects" ADD COLUMN "cost_center_id" text`); err != nil {
			return fmt.Errorf("add gateway project cost center column: %w", err)
		}
	}
	for table, columns := range map[string]string{
		"gateway_cost_centers":              `id text PRIMARY KEY, tenant_id text, external_cost_center_id text, code text, name text, status text, version integer, synced_at datetime, deleted_at datetime, created_at datetime, updated_at datetime`,
		"gateway_organization_cost_centers": `id text PRIMARY KEY, tenant_id text, external_binding_id text, organization_id text, cost_center_id text, status text, version integer, synced_at datetime, deleted_at datetime, created_at datetime, updated_at datetime`,
	} {
		if _, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+table+" ("+columns+")"); err != nil {
			return fmt.Errorf("create gateway cost center projection table %s: %w", table, err)
		}
	}
	for _, index := range gatewayCostCenterIndexes {
		if err := createSchemaIndex(ctx, db, index.table, index.name, index.column, index.unique); err != nil {
			return err
		}
	}
	return createSchemaIndex(ctx, db, "gateway_projects", "idx_gateway_projects_cost_center_id", "cost_center_id", false)
}

var gatewayCostCenterIndexes = []struct {
	table  string
	name   string
	column string
	unique bool
}{
	{"gateway_cost_centers", "idx_gateway_cost_center_external", "tenant_id, external_cost_center_id", true},
	{"gateway_cost_centers", "idx_gateway_cost_centers_tenant_id", "tenant_id", false},
	{"gateway_cost_centers", "idx_gateway_cost_centers_status", "status", false},
	{"gateway_cost_centers", "idx_gateway_cost_centers_deleted_at", "deleted_at", false},
	{"gateway_organization_cost_centers", "idx_gateway_org_cost_center_external", "tenant_id, external_binding_id", true},
	{"gateway_organization_cost_centers", "idx_gateway_org_cost_centers_tenant_id", "tenant_id", false},
	{"gateway_organization_cost_centers", "idx_gateway_org_cost_centers_organization_id", "organization_id", false},
	{"gateway_organization_cost_centers", "idx_gateway_org_cost_centers_cost_center_id", "cost_center_id", false},
	{"gateway_organization_cost_centers", "idx_gateway_org_cost_centers_status", "status", false},
	{"gateway_organization_cost_centers", "idx_gateway_org_cost_centers_deleted_at", "deleted_at", false},
}
