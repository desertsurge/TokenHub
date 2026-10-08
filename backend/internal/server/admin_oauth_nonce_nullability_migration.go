package server

import (
	"context"
	"fmt"

	"tokenhub/backend/internal/dbschema"
)

func adminOAuthNonceNullabilityMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          18,
		Name:             "normalize-admin-oauth-nonce-nullability",
		Go:               normalizeAdminOAuthNonceNullability,
		ChecksumOverride: "tokenhub-schema-admin-oauth-nonce-nullability-v1",
		StatementBudget:  8,
	}
}

func normalizeAdminOAuthNonceNullability(ctx context.Context, db dbschema.MigrationExecer) error {
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err == nil {
		_, err := db.ExecContext(ctx, `ALTER TABLE admin_o_auth_oidc_nonces ALTER COLUMN nonce DROP NOT NULL`)
		return err
	}

	var notNull int
	if err := db.QueryRowContext(ctx, `SELECT "notnull" FROM pragma_table_info('admin_o_auth_oidc_nonces') WHERE name = 'nonce'`).Scan(&notNull); err != nil {
		return fmt.Errorf("inspect admin OAuth nonce nullability: %w", err)
	}
	if notNull == 0 {
		return nil
	}
	for _, statement := range []string{
		`CREATE TABLE admin_o_auth_oidc_nonces_nullable (flow_id text PRIMARY KEY, nonce text)`,
		`INSERT INTO admin_o_auth_oidc_nonces_nullable (flow_id, nonce) SELECT flow_id, nonce FROM admin_o_auth_oidc_nonces`,
		`DROP TABLE admin_o_auth_oidc_nonces`,
		`ALTER TABLE admin_o_auth_oidc_nonces_nullable RENAME TO admin_o_auth_oidc_nonces`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("normalize admin OAuth nonce nullability: %w", err)
		}
	}
	return nil
}
