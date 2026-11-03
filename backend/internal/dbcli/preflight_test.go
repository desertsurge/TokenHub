package dbcli

import (
	"os"
	"strings"
	"testing"
)

func TestPreflightHelpDoesNotConnectToDatabase(t *testing.T) {
	t.Setenv("TOKENHUB_DATABASE_URL", "postgres://invalid:invalid@127.0.0.1:1/missing")
	code, output := runCLI(t, "preflight", "--help")
	if code != 0 || !strings.Contains(output, "read-only PostgreSQL migration ledger") {
		t.Fatalf("preflight help: code=%d output=%q", code, output)
	}
}

func TestPreflightRejectsSQLiteWithoutCreatingFile(t *testing.T) {
	databaseURL := cliTestEnv(t)
	code, output := runCLI(t, "preflight")
	if code != 1 || strings.Contains(output, "baseline recorded:") {
		t.Fatalf("preflight accepted unsupported database: code=%d output=%q", code, output)
	}
	if _, err := os.Stat(strings.TrimPrefix(databaseURL, "sqlite://")); !os.IsNotExist(err) {
		t.Fatalf("preflight created a database file: %v", err)
	}
}
