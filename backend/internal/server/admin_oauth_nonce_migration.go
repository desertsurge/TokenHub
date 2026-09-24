package server

import (
	"context"

	"tokenhub/backend/internal/dbschema"
)

type adminOAuthOIDCNonce struct {
	FlowID string `gorm:"primaryKey"`
	Nonce  string
}

func (adminOAuthOIDCNonce) TableName() string { return "admin_o_auth_oidc_nonces" }

func adminOAuthNonceMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          10,
		Name:             "add-admin-oauth-oidc-nonce",
		Go:               addAdminOAuthOIDCNonce,
		ChecksumOverride: "tokenhub-schema-admin-oauth-oidc-nonce-v1",
		StatementBudget:  1,
	}
}

func addAdminOAuthOIDCNonce(ctx context.Context, db dbschema.MigrationExecer) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS admin_o_auth_oidc_nonces (flow_id text PRIMARY KEY, nonce text NOT NULL)`)
	return err
}
