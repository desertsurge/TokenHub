package server

import (
	"context"
	"fmt"

	"tokenhub/backend/internal/dbschema"
)

func gatewayOrganizationPrimaryMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          12,
		Name:             "add-gateway-organization-primary",
		Go:               addGatewayOrganizationPrimary,
		ChecksumOverride: "tokenhub-schema-gateway-organization-primary-v1",
		StatementBudget:  3,
	}
}

func addGatewayOrganizationPrimary(ctx context.Context, db dbschema.MigrationExecer) error {
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err == nil {
		if _, err := db.ExecContext(ctx, `ALTER TABLE gateway_principal_organization_bindings ADD COLUMN IF NOT EXISTS is_primary boolean NOT NULL DEFAULT false`); err != nil {
			return fmt.Errorf("add gateway organization primary column: %w", err)
		}
		return nil
	}
	exists, err := sqliteColumnExists(ctx, db, "gateway_principal_organization_bindings", "is_primary")
	if err != nil {
		return fmt.Errorf("inspect gateway organization primary column: %w", err)
	}
	if !exists {
		if _, err := db.ExecContext(ctx, `ALTER TABLE "gateway_principal_organization_bindings" ADD COLUMN "is_primary" numeric NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add gateway organization primary column: %w", err)
		}
	}
	return nil
}
