package server

import (
	"context"
	"fmt"

	"tokenhub/backend/internal/dbschema"
)

func usageCostCenterSnapshotMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          13,
		Name:             "add-usage-cost-center-snapshot",
		Go:               addUsageCostCenterSnapshot,
		ChecksumOverride: "tokenhub-schema-usage-cost-center-snapshot-v1",
		StatementBudget:  10,
	}
}

func addUsageCostCenterSnapshot(ctx context.Context, db dbschema.MigrationExecer) error {
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err == nil {
		if _, err := db.ExecContext(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS cost_center_snapshot text`); err != nil {
			return fmt.Errorf("add usage cost center snapshot column: %w", err)
		}
	} else {
		exists, err := sqliteColumnExists(ctx, db, "usage_records", "cost_center_snapshot")
		if err != nil {
			return fmt.Errorf("inspect usage cost center snapshot column: %w", err)
		}
		if !exists {
			if _, err := db.ExecContext(ctx, `ALTER TABLE "usage_records" ADD COLUMN "cost_center_snapshot" text`); err != nil {
				return fmt.Errorf("add usage cost center snapshot column: %w", err)
			}
		}
	}
	return createSchemaIndex(ctx, db, "usage_records", "idx_usage_records_cost_center_snapshot", "cost_center_snapshot", false)
}
