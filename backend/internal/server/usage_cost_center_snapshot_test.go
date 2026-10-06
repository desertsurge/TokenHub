package server

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUsageCostCenterSnapshotSurvivesProjectReassignment(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "snapshot.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migrateSchemaObjects(database, "sqlite"); err != nil {
		t.Fatal(err)
	}
	store := &GormStore{db: database, mu: &sync.Mutex{}}
	project := store.CreateProject(Project{Name: "Snapshot Project", CostCenter: "CC-OLD"})
	period := time.Now().UTC().Format("2006-01")
	costCenter := "CC-OLD"
	record := UsageRecord{
		ID: "usage-snapshot", RequestID: "request-snapshot", ProjectID: project.ID,
		CostCenterSnapshot: &costCenter, TotalTokens: 10, CostUSD: 1.25, CreatedAt: time.Now().UTC(),
	}
	if err := store.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateProject(project.ID, Project{Name: project.Name, CostCenter: "CC-NEW", Status: StatusActive}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GenerateBillingPeriod(period); err != nil {
		t.Fatal(err)
	}
	chargebacks := store.ListResources("chargebacks")
	if len(chargebacks) != 1 || stringField(chargebacks[0].Fields, "cost_center") != "CC-OLD" {
		t.Fatalf("chargebacks changed attribution after project reassignment: %+v", chargebacks)
	}
	breakdown := (&Server{store: store}).usageBreakdownFromRecords(store.ListUsageRecords(), indexProjectsByID(store.ListProjects()))
	items, ok := breakdown["cost_centers"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["id"] != "CC-OLD" {
		t.Fatalf("usage breakdown changed attribution after project reassignment: %+v", breakdown["cost_centers"])
	}
	totals, err := store.aggregateRuntimeBudgetTotals(store.db, period, Project{ID: project.ID, CostCenter: "CC-NEW"}, runtimeBudgetScopes{costCenter: true}, "CC-OLD", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if totals.costCenter != 1.25 {
		t.Fatalf("historic cost center budget total = %v, want 1.25", totals.costCenter)
	}
}

func TestNewUsageRecordCapturesCostCenter(t *testing.T) {
	costCenter := "CC-AT-REQUEST"
	record := newUsageRecord(CallContext{}, RouteSelection{}, Usage{}, costCenter, time.Now().UTC())
	if record.CostCenterSnapshot == nil || *record.CostCenterSnapshot != costCenter {
		t.Fatalf("cost center snapshot = %v, want %s", record.CostCenterSnapshot, costCenter)
	}
}

func TestUsageCostCenterSnapshotIsResolvedAtAdmission(t *testing.T) {
	store, _ := newRateLimitedGateway(t, APIKey{})
	team := store.CreateResource("teams", AdminResource{
		Name: "Mutable cost center team", Status: StatusActive, Fields: map[string]any{"cost_center": "CC-ADMISSION"},
	})
	project := store.ListProjects()[0]
	project, err := store.UpdateProject(project.ID, Project{Name: project.Name, TeamID: team.ID, Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, store.ListAPIKeys()[0], "gpt-4.1-mini", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateResource("teams", team.ID, AdminResource{
		Name: team.Name, Status: StatusActive, Fields: map[string]any{"cost_center": "CC-SETTLEMENT"},
	}); err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 1}, http.StatusOK, "", "127.0.0.1", "snapshot-test")

	records := store.ListUsageRecords()
	if len(records) != 1 || records[0].CostCenterSnapshot == nil || *records[0].CostCenterSnapshot != "CC-ADMISSION" {
		t.Fatalf("usage did not preserve the admission cost center: %+v", records)
	}
}

func TestCostCenterSnapshotResolutionOnlyFallsBackForMissingResources(t *testing.T) {
	store := NewMemoryStore()
	project := Project{ID: "project-fallback", TeamID: "missing-team", DefaultQuotaRef: "missing-quota"}
	costCenter, err := store.costCenterForProjectWithDB(store.db, project)
	if err != nil || costCenter != "missing-team" {
		t.Fatalf("missing resources should use the documented fallback: cost center=%q err=%v", costCenter, err)
	}

	if err := store.db.Migrator().DropTable(&AdminResource{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.costCenterForProjectWithDB(store.db, project); err == nil {
		t.Fatal("database read failure was treated as a missing cost-center resource")
	}
}
