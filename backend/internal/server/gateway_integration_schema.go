package server

import (
	"context"
	"fmt"
	"strings"

	"tokenhub/backend/internal/dbschema"
)

// gatewayIntegrationMigration adds the enterprise gateway integration schema
// (tenant, organization, principal, project, and workload projections, the
// integration inbox, and the gateway scoping columns on api_keys) to databases
// adopted before those models existed. Every statement is idempotent so
// manually repaired databases accept the migration without changes.
func gatewayIntegrationMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          7,
		Name:             "add-gateway-integration-schema",
		Go:               addGatewayIntegrationSchema,
		ChecksumOverride: "tokenhub-schema-gateway-integration-v1",
		StatementBudget:  100,
	}
}

// gatewayIntegrationStorageMigration completes fields and indexes used by the
// credential and analytics paths that were not included in version 7.
func gatewayIntegrationStorageMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          8,
		Name:             "complete-gateway-integration-schema",
		Go:               completeGatewayIntegrationSchema,
		ChecksumOverride: "tokenhub-schema-gateway-integration-storage-v1",
		StatementBudget:  10,
	}
}

var gatewayAPIKeyColumns = []string{
	"tenant_external_id", "project_external_id", "principal_type", "principal_external_id",
	"environment", "managed_by", "control_request_id", "control_request_digest",
}

// gatewayTableColumns is neutral (SQLite-affinity) DDL for the gateway
// projection tables; postgresType upgrades it for PostgreSQL.
var gatewayTableColumns = map[string][]string{
	"gateway_tenants": {
		"id text PRIMARY KEY", "external_tenant_id text", "name text", "status text",
		"version integer", "synced_at datetime", "deleted_at datetime",
		"created_at datetime", "updated_at datetime",
	},
	"gateway_organizations": {
		"id text PRIMARY KEY", "tenant_id text", "external_organization_id text", "parent_id text",
		"name text", "status text", "version integer", "synced_at datetime",
		"deleted_at datetime", "created_at datetime", "updated_at datetime",
	},
	"gateway_principals": {
		"id text PRIMARY KEY", "tenant_id text", "external_principal_id text",
		"external_membership_id text", "display_name text", "status text",
		"version integer", "source_occurred_at datetime", "synced_at datetime",
		"deleted_at datetime", "created_at datetime", "updated_at datetime",
	},
	"gateway_principal_organization_bindings": {
		"id text PRIMARY KEY", "tenant_id text", "external_membership_id text",
		"principal_id text", "organization_id text", "status text",
		"version integer", "synced_at datetime", "deleted_at datetime",
		"created_at datetime", "updated_at datetime",
	},
	"gateway_projects": {
		"id text PRIMARY KEY", "tenant_id text", "external_project_id text",
		"organization_id text", "owner_principal_id text", "cost_center_id text", "name text", "status text",
		"version integer", "synced_at datetime", "deleted_at datetime",
		"created_at datetime", "updated_at datetime",
	},
	"gateway_cost_centers": {
		"id text PRIMARY KEY", "tenant_id text", "external_cost_center_id text", "code text", "name text", "status text",
		"version integer", "synced_at datetime", "deleted_at datetime", "created_at datetime", "updated_at datetime",
	},
	"gateway_organization_cost_centers": {
		"id text PRIMARY KEY", "tenant_id text", "external_binding_id text", "organization_id text", "cost_center_id text", "status text",
		"version integer", "synced_at datetime", "deleted_at datetime", "created_at datetime", "updated_at datetime",
	},
	"gateway_workloads": {
		"id text PRIMARY KEY", "tenant_id text", "external_workload_id text", "project_id text",
		"owner_principal_id text", "name text", "workload_type text", "environment text",
		"status text", "version integer", "synced_at datetime", "deleted_at datetime",
		"created_at datetime", "updated_at datetime",
	},
	"integration_inbox": {
		"event_id text PRIMARY KEY", "event_digest text", "tenant_id text", "event_type text",
		"aggregate_type text", "aggregate_id text", "aggregate_version integer",
		"applied_version integer", "status text", "entity_type text",
		"token_hub_entity_id text", "received_at datetime", "processed_at datetime",
		"created_at datetime", "updated_at datetime",
	},
}

// gatewayTableIndexes mirrors the GORM index tags of the gateway models so the
// migrated schema matches the schema AutoMigrate creates on fresh databases.
var gatewayTableIndexes = []struct {
	table  string
	name   string
	column string
	unique bool
}{
	{"gateway_tenants", "idx_gateway_tenants_external_tenant_id", "external_tenant_id", true},
	{"gateway_tenants", "idx_gateway_tenants_status", "status", false},
	{"gateway_tenants", "idx_gateway_tenants_deleted_at", "deleted_at", false},
	{"gateway_organizations", "idx_gateway_org_external", "tenant_id, external_organization_id", true},
	{"gateway_organizations", "idx_gateway_organizations_tenant_id", "tenant_id", false},
	{"gateway_organizations", "idx_gateway_organizations_parent_id", "parent_id", false},
	{"gateway_organizations", "idx_gateway_organizations_status", "status", false},
	{"gateway_organizations", "idx_gateway_organizations_deleted_at", "deleted_at", false},
	{"gateway_principals", "idx_gateway_principal_external", "tenant_id, external_principal_id", true},
	{"gateway_principals", "idx_gateway_principals_tenant_id", "tenant_id", false},
	{"gateway_principals", "idx_gateway_principals_external_membership_id", "external_membership_id", false},
	{"gateway_principals", "idx_gateway_principals_status", "status", false},
	{"gateway_principals", "idx_gateway_principals_deleted_at", "deleted_at", false},
	{"gateway_principal_organization_bindings", "idx_gateway_org_binding_external", "tenant_id, external_membership_id", true},
	{"gateway_principal_organization_bindings", "idx_gateway_principal_organization_bindings_tenant_id", "tenant_id", false},
	{"gateway_principal_organization_bindings", "idx_gateway_principal_organization_bindings_principal_id", "principal_id", false},
	{"gateway_principal_organization_bindings", "idx_gateway_principal_organization_bindings_organization_id", "organization_id", false},
	{"gateway_principal_organization_bindings", "idx_gateway_principal_organization_bindings_status", "status", false},
	{"gateway_principal_organization_bindings", "idx_gateway_principal_organization_bindings_deleted_at", "deleted_at", false},
	{"gateway_projects", "idx_gateway_project_external", "tenant_id, external_project_id", true},
	{"gateway_projects", "idx_gateway_projects_tenant_id", "tenant_id", false},
	{"gateway_projects", "idx_gateway_projects_organization_id", "organization_id", false},
	{"gateway_projects", "idx_gateway_projects_owner_principal_id", "owner_principal_id", false},
	{"gateway_projects", "idx_gateway_projects_cost_center_id", "cost_center_id", false},
	{"gateway_projects", "idx_gateway_projects_status", "status", false},
	{"gateway_projects", "idx_gateway_projects_deleted_at", "deleted_at", false},
	{"gateway_workloads", "idx_gateway_workload_external", "tenant_id, external_workload_id", true},
	{"gateway_workloads", "idx_gateway_workloads_tenant_id", "tenant_id", false},
	{"gateway_workloads", "idx_gateway_workloads_project_id", "project_id", false},
	{"gateway_workloads", "idx_gateway_workloads_owner_principal_id", "owner_principal_id", false},
	{"gateway_workloads", "idx_gateway_workloads_environment", "environment", false},
	{"gateway_workloads", "idx_gateway_workloads_status", "status", false},
	{"gateway_workloads", "idx_gateway_workloads_deleted_at", "deleted_at", false},
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
	{"integration_inbox", "idx_integration_inbox_tenant_id", "tenant_id", false},
	{"integration_inbox", "idx_integration_inbox_aggregate", "aggregate_type, aggregate_id, aggregate_version", false},
	{"integration_inbox", "idx_integration_inbox_status", "status", false},
}

// gatewayAPIKeyIndexes mirrors the GORM index tags added to APIKey by the
// enterprise integration models.
var gatewayAPIKeyIndexes = []struct {
	name   string
	column string
	unique bool
}{
	{"idx_api_key_gateway_scope", "tenant_external_id, project_external_id", false},
	{"idx_api_keys_principal_type", "principal_type", false},
	{"idx_api_keys_principal_external_id", "principal_external_id", false},
	{"idx_api_keys_environment", "environment", false},
	{"idx_api_keys_managed_by", "managed_by", false},
	{"idx_api_keys_control_request_id", "control_request_id", true},
}

func addGatewayIntegrationSchema(ctx context.Context, db dbschema.MigrationExecer) error {
	// current_schema() exists on PostgreSQL; SQLite fails the probe without
	// aborting the migration transaction (same pattern as v5).
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err == nil {
		return addGatewayIntegrationSchemaPostgres(ctx, db)
	}
	return addGatewayIntegrationSchemaSQLite(ctx, db)
}

func completeGatewayIntegrationSchema(ctx context.Context, db dbschema.MigrationExecer) error {
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err == nil {
		if _, err := db.ExecContext(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_ciphertext text`); err != nil {
			return fmt.Errorf("add encrypted key storage to api_keys: %w", err)
		}
	} else {
		exists, inspectErr := sqliteColumnExists(ctx, db, "api_keys", "key_ciphertext")
		if inspectErr != nil {
			return fmt.Errorf("inspect api_keys column key_ciphertext: %w", inspectErr)
		}
		if !exists {
			if _, alterErr := db.ExecContext(ctx, `ALTER TABLE "api_keys" ADD COLUMN "key_ciphertext" text`); alterErr != nil {
				return fmt.Errorf("add encrypted key storage to api_keys: %w", alterErr)
			}
		}
	}
	return createSchemaIndex(ctx, db, "usage_records", "idx_usage_request_key", "request_id, api_key_id", false)
}

func addGatewayIntegrationSchemaPostgres(ctx context.Context, db dbschema.MigrationExecer) error {
	adds := make([]string, 0, len(gatewayAPIKeyColumns))
	for _, column := range gatewayAPIKeyColumns {
		adds = append(adds, "ADD COLUMN IF NOT EXISTS "+column+" text")
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE api_keys "+strings.Join(adds, ",\n\t")); err != nil {
		return fmt.Errorf("add gateway scoping columns to api_keys: %w", err)
	}
	for _, index := range gatewayAPIKeyIndexes {
		if err := createSchemaIndex(ctx, db, "api_keys", index.name, index.column, index.unique); err != nil {
			return err
		}
	}
	for table, columns := range gatewayTableColumns {
		postgresColumns := make([]string, 0, len(columns))
		for _, column := range columns {
			postgresColumns = append(postgresColumns, postgresColumnType(column))
		}
		if _, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+table+" ("+strings.Join(postgresColumns, ",\n\t")+")"); err != nil {
			return fmt.Errorf("create gateway table %s: %w", table, err)
		}
	}
	for _, index := range gatewayTableIndexes {
		if err := createSchemaIndex(ctx, db, index.table, index.name, index.column, index.unique); err != nil {
			return err
		}
	}
	return nil
}

func addGatewayIntegrationSchemaSQLite(ctx context.Context, db dbschema.MigrationExecer) error {
	for _, column := range gatewayAPIKeyColumns {
		exists, err := sqliteColumnExists(ctx, db, "api_keys", column)
		if err != nil {
			return fmt.Errorf("inspect api_keys column %s: %w", column, err)
		}
		if exists {
			continue
		}
		if _, err := db.ExecContext(ctx, `ALTER TABLE "api_keys" ADD COLUMN `+quoteSQLiteIdent(column)+" text"); err != nil {
			return fmt.Errorf("add api_keys column %s: %w", column, err)
		}
	}
	for _, index := range gatewayAPIKeyIndexes {
		if err := createSchemaIndex(ctx, db, "api_keys", index.name, index.column, index.unique); err != nil {
			return err
		}
	}
	for table, columns := range gatewayTableColumns {
		if _, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+table+" ("+strings.Join(columns, ",\n\t")+")"); err != nil {
			return fmt.Errorf("create gateway table %s: %w", table, err)
		}
	}
	for _, index := range gatewayTableIndexes {
		if err := createSchemaIndex(ctx, db, index.table, index.name, index.column, index.unique); err != nil {
			return err
		}
	}
	return nil
}

func createSchemaIndex(ctx context.Context, db dbschema.MigrationExecer, table, name, columns string, unique bool) error {
	kind := "INDEX"
	if unique {
		kind = "UNIQUE INDEX"
	}
	if _, err := db.ExecContext(ctx, "CREATE "+kind+" IF NOT EXISTS "+name+" ON "+table+" ("+columns+")"); err != nil {
		return fmt.Errorf("create gateway index %s: %w", name, err)
	}
	return nil
}

// postgresColumnType upgrades the neutral column definitions to PostgreSQL
// types (integer -> bigint, datetime -> timestamptz).
func postgresColumnType(column string) string {
	switch {
	case column == "version integer":
		return "version bigint"
	case column == "aggregate_version integer":
		return "aggregate_version bigint"
	case column == "applied_version integer":
		return "applied_version bigint"
	case strings.HasSuffix(column, " datetime"):
		return strings.TrimSuffix(column, " datetime") + " timestamptz"
	}
	return column
}

func quoteSQLiteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
