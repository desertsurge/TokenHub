package dbcli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"tokenhub/backend/internal/dbschema"
	"tokenhub/backend/internal/server"
)

func runPreflight(ctx context.Context, args []string, config server.Config, stdout io.Writer) error {
	if len(args) == 1 && args[0] == "--help" {
		_, err := fmt.Fprintln(stdout, "usage: tokenhub db preflight (read-only PostgreSQL migration ledger)")
		return err
	}
	if len(args) != 0 {
		return errors.New("preflight does not accept arguments")
	}
	db, err := sql.Open("pgx", config.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	runner, err := dbschema.NewRunner(db, dbschema.DialectPostgres, server.SchemaMigrationRegistry())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	status, err := runner.Preflight(ctx)
	if err != nil {
		return err
	}
	printMigrationStatus(stdout, string(dbschema.DialectPostgres), status)
	return nil
}
