package dbschema

import (
	"context"
	"database/sql"
	"fmt"
)

// Preflight verifies an existing migration ledger and reports the target
// registry's pending migrations without initializing or repairing anything.
func (r *Runner) Preflight(ctx context.Context) (Status, error) {
	options := &sql.TxOptions{ReadOnly: true}
	if r.dialect == DialectPostgres {
		options.Isolation = sql.LevelRepeatableRead
	}
	tx, err := r.db.BeginTx(ctx, options)
	if err != nil {
		return Status{}, fmt.Errorf("dbschema: begin read-only preflight: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	query := "SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations')"
	if r.dialect == DialectPostgres {
		query = "SELECT to_regclass('schema_migrations') IS NOT NULL"
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, query).Scan(&exists); err != nil {
		return Status{}, fmt.Errorf("dbschema: inspect migration ledger: %w", err)
	}
	if !exists {
		return Status{}, newError(ErrCodeBaselineMissing, BaselineVersion, errNoBaseline)
	}
	applied, err := loadAppliedOn(ctx, tx)
	if err != nil {
		return Status{}, err
	}
	if findApplied(applied, BaselineVersion) == nil {
		return Status{}, newError(ErrCodeBaselineMissing, BaselineVersion, errNoBaseline)
	}
	if err := r.verifyApplied(applied); err != nil {
		return Status{}, err
	}
	return r.statusFromApplied(applied), nil
}
