package server

import (
	"context"
	"fmt"

	"tokenhub/backend/internal/dbschema"
)

func gatewayPrincipalEmailMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          16,
		Name:             "add-gateway-principal-email",
		Go:               addGatewayPrincipalEmail,
		ChecksumOverride: "tokenhub-schema-gateway-principal-email-v1",
		StatementBudget:  3,
	}
}

func addGatewayPrincipalEmail(ctx context.Context, db dbschema.MigrationExecer) error {
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err == nil {
		if _, err := db.ExecContext(ctx, `ALTER TABLE gateway_principals ADD COLUMN IF NOT EXISTS email text`); err != nil {
			return fmt.Errorf("add gateway principal email column: %w", err)
		}
		return nil
	}
	exists, err := sqliteColumnExists(ctx, db, "gateway_principals", "email")
	if err != nil {
		return fmt.Errorf("inspect gateway principal email column: %w", err)
	}
	if !exists {
		if _, err := db.ExecContext(ctx, `ALTER TABLE "gateway_principals" ADD COLUMN "email" text`); err != nil {
			return fmt.Errorf("add gateway principal email column: %w", err)
		}
	}
	return nil
}
