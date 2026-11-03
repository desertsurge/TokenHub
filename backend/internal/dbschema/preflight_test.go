package dbschema

import (
	"context"
	"testing"
)

func TestPreflightDoesNotInitializeLedger(t *testing.T) {
	db := newTestDB(t)
	r := mustRunner(t, db, nil)
	_, err := r.Preflight(context.Background())
	requireErrorCode(t, err, ErrCodeBaselineMissing)
	for _, table := range []string{"schema_migrations", "migration_attempts", "data_backfills"} {
		if tableExists(t, db, table) {
			t.Fatalf("preflight created %s", table)
		}
	}
}

func TestPreflightReportsPendingAndRejectsChecksumDrift(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := mustRunner(t, db, nil).Adopt(ctx, nil); err != nil {
		t.Fatal(err)
	}
	r := mustRunner(t, db, []Migration{
		{Version: 2, Name: "expand", Statements: []string{"CREATE TABLE demo (id INTEGER)"}},
		{Version: 3, Name: "contract", Phase: PhaseContract, Statements: []string{"DROP TABLE demo"}},
	})
	status, err := r.Preflight(ctx)
	if err != nil || !status.BaselineRecorded || status.CurrentVersion != BaselineVersion ||
		len(status.PendingExpand) != 1 || len(status.PendingContract) != 1 || tableExists(t, db, "demo") {
		t.Fatalf("preflight status=%+v err=%v", status, err)
	}
	if _, err := db.Exec("UPDATE schema_migrations SET checksum = 'tampered'"); err != nil {
		t.Fatal(err)
	}
	_, err = r.Preflight(ctx)
	requireErrorCode(t, err, ErrCodeChecksumMismatch)
}
