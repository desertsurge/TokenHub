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
		// Three tables may each need an index plus two idempotent backfill
		// statements, with a probe and ALTER on SQLite when the column is absent.
		StatementBudget: 24,
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
			if err := backfillTenantExternalIDSQL(ctx, db, table); err != nil {
				return err
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
		if err := backfillTenantExternalIDSQL(ctx, db, table); err != nil {
			return err
		}
	}
	return nil
}

func backfillTenantExternalIDSQL(ctx context.Context, db dbschema.MigrationExecer, table string) error {
	keyColumn := "api_key_id"
	unknownKeyFilter := ""
	if table == "quota_buckets" {
		keyColumn = "key_id"
		unknownKeyFilter = "  AND key_id NOT LIKE 'user:%'\n"
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
UPDATE %s
SET tenant_external_id = (
    SELECT tenant_external_id FROM api_keys WHERE api_keys.id = %s.%s
)
WHERE (tenant_external_id IS NULL OR tenant_external_id = '' OR tenant_external_id = ?)
  AND EXISTS (SELECT 1 FROM api_keys WHERE api_keys.id = %s.%s AND api_keys.tenant_external_id <> '')`, table, table, keyColumn, table, keyColumn), unattributedTenantExternalID); err != nil {
		return fmt.Errorf("backfill tenant column on %s: %w", table, err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
UPDATE %s
SET tenant_external_id = ?
WHERE (tenant_external_id IS NULL OR tenant_external_id = '')
%s  AND NOT EXISTS (SELECT 1 FROM api_keys WHERE api_keys.id = %s.%s)`, table, unknownKeyFilter, table, keyColumn), unattributedTenantExternalID); err != nil {
		return fmt.Errorf("mark unassigned tenant on %s: %w", table, err)
	}
	return nil
}
