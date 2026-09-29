package server

import (
	"context"
	"fmt"

	"tokenhub/backend/internal/dbschema"
)

// quotaBucketTenantMigration persists tenant ownership on usage buckets so
// user quota history remains tenant-scoped after an API key is deleted.
func quotaBucketTenantMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          14,
		Name:             "add-quota-bucket-tenant-scope",
		Go:               addQuotaBucketTenantScope,
		ChecksumOverride: "tokenhub-schema-quota-bucket-tenant-scope-v1",
		StatementBudget:  18,
	}
}

func addQuotaBucketTenantScope(ctx context.Context, db dbschema.MigrationExecer) error {
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err == nil {
		for _, table := range []string{"quota_buckets", "image_jobs", "response_jobs"} {
			if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS tenant_external_id text`, table)); err != nil {
				return fmt.Errorf("add tenant column to %s: %w", table, err)
			}
			if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_tenant_external_id ON %s (tenant_external_id)`, table, table)); err != nil {
				return fmt.Errorf("create tenant index for %s: %w", table, err)
			}
		}
		return nil
	}

	for _, table := range []string{"quota_buckets", "image_jobs", "response_jobs"} {
		if exists, err := sqliteColumnExists(ctx, db, table, "tenant_external_id"); err != nil {
			return fmt.Errorf("inspect tenant column on %s: %w", table, err)
		} else if !exists {
			if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %q ADD COLUMN tenant_external_id text`, table)); err != nil {
				return fmt.Errorf("add tenant column to %s: %w", table, err)
			}
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_tenant_external_id ON %s (tenant_external_id)`, table, table)); err != nil {
			return fmt.Errorf("create tenant index for %s: %w", table, err)
		}
	}
	return nil
}
