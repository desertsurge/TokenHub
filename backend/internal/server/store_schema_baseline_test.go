package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"tokenhub/backend/internal/dbschema"
)

func TestSQLiteBaselineUpgradesToCurrentSchema(t *testing.T) {
	ctx := context.Background()
	baseline, err := dbschema.SQLiteBaselineStatements()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range baseline {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("replay frozen baseline: %v", err)
		}
	}
	runner, err := dbschema.NewRunner(db, dbschema.DialectSQLite, SchemaMigrationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("upgrade frozen baseline: %v", err)
	}
	reference, err := buildSchemaReferenceWithExpansions(ctx, "sqlite", "", true)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := dbschema.Introspect(ctx, db, dbschema.DialectSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	if violations := dbschema.CompareObjects(reference, actual); len(violations) > 0 {
		t.Fatalf("upgraded baseline differs from current schema: %s", dbschema.FormatViolations(violations))
	}
}
