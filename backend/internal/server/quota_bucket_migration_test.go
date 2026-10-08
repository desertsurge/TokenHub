package server

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func TestQuotaBucketMigrationPreservesUnknownAndUserScopedHistory(t *testing.T) {
	store := NewMemoryStore()
	if err := store.db.Exec("DROP TABLE quota_buckets").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec(`CREATE TABLE quota_buckets (
key_id TEXT NOT NULL,
scope TEXT NOT NULL,
bucket TEXT NOT NULL,
attributed_user_id TEXT,
requests INTEGER NOT NULL DEFAULT 0,
prompt_tokens INTEGER NOT NULL DEFAULT 0,
completion_tokens INTEGER NOT NULL DEFAULT 0,
total_tokens INTEGER NOT NULL DEFAULT 0,
cost_usd REAL NOT NULL DEFAULT 0,
PRIMARY KEY (key_id, scope, bucket)
)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("INSERT INTO quota_buckets (key_id, scope, bucket, requests, total_tokens) VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)", "key_legacy", "day", "2026-08-20", 4, 12, "user:usr_legacy", "day", "2026-08-20", 2, 8).Error; err != nil {
		t.Fatal(err)
	}
	if err := ensureQuotaBucketAttributionSchema(store.db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := addQuotaBucketTenantScope(context.Background(), directSQLMigrationExecer{DB: sqlDB}); err != nil {
		t.Fatal(err)
	}
	var keyRow, userRow QuotaBucket
	if err := store.db.Where("key_id = ?", "key_legacy").First(&keyRow).Error; err != nil {
		t.Fatal(err)
	}
	if keyRow.AttributedUserID != unattributedQuotaUserID || keyRow.TotalTokens != 12 {
		t.Fatalf("legacy key history = %+v", keyRow)
	}
	if err := store.db.Where("key_id = ?", "user:usr_legacy").First(&userRow).Error; err != nil {
		t.Fatal(err)
	}
	if userRow.AttributedUserID != "usr_legacy" || userRow.TotalTokens != 8 {
		t.Fatalf("legacy user history = %+v", userRow)
	}
	aggregate, err := store.aggregateUserQuotaCounter(store.db, "usr_legacy", "day", "2026-08-20")
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.TotalTokens != 0 {
		t.Fatalf("unattributed legacy key history was assigned to user: %+v", aggregate)
	}
	if err := store.db.Exec("INSERT INTO quota_buckets (key_id, scope, bucket, requests) VALUES (?, ?, ?, ?)", "key_rollback", "day", "2026-08-20", 1).Error; err != nil {
		t.Fatalf("simulate an older release writing without attribution: %v", err)
	}
	var rollbackRow QuotaBucket
	if err := store.db.First(&rollbackRow, "key_id = ?", "key_rollback").Error; err != nil {
		t.Fatal(err)
	}
	if rollbackRow.AttributedUserID != unattributedQuotaUserID {
		t.Fatalf("older-release write attribution = %q, want %q", rollbackRow.AttributedUserID, unattributedQuotaUserID)
	}
}

func TestStoreStartupAdoptsLegacyQuotaBucketSchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy-quota.db")
	databaseURL := "sqlite://" + databasePath
	store, err := NewStoreWithDialect(databaseURL, Config{})
	if err != nil {
		t.Fatalf("create current store: %v", err)
	}
	if err := store.db.Exec("DROP TABLE quota_buckets").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec(`CREATE TABLE quota_buckets (
key_id TEXT NOT NULL,
scope TEXT NOT NULL,
bucket TEXT NOT NULL,
requests INTEGER NOT NULL DEFAULT 0,
prompt_tokens INTEGER NOT NULL DEFAULT 0,
completion_tokens INTEGER NOT NULL DEFAULT 0,
total_tokens INTEGER NOT NULL DEFAULT 0,
cost_usd REAL NOT NULL DEFAULT 0,
PRIMARY KEY (key_id, scope, bucket)
)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("INSERT INTO quota_buckets (key_id, scope, bucket, total_tokens) VALUES (?, ?, ?, ?)", "key_legacy", "day", "2026-08-20", 12).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("DROP TABLE schema_migrations").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("DROP TABLE migration_attempts").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	upgraded, err := NewStoreWithDialect(databaseURL, Config{})
	if err != nil {
		t.Fatalf("adopt legacy quota schema through startup: %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	var row QuotaBucket
	if err := upgraded.db.First(&row, "key_id = ?", "key_legacy").Error; err != nil {
		t.Fatal(err)
	}
	if row.AttributedUserID != unattributedQuotaUserID || row.TotalTokens != 12 {
		t.Fatalf("adopted legacy quota row = %+v", row)
	}
}

func TestBackfillAsyncJobTenantAttribution(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_job_tenant", Name: "Job tenant", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_job_tenant", TenantExternalID: "tenant-job", Status: StatusActive}, "thk_job_tenant")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&ImageJob{ID: "img_job_tenant", ProjectID: project.ID, APIKeyID: key.ID, Status: imageJobStatusQueued}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&ResponseJob{ID: "resp_job_tenant", ProjectID: project.ID, APIKeyID: key.ID, Status: responseJobStatusQueued, Phase: responseJobPhaseQueued}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&ResponseJob{ID: "resp_job_unknown", ProjectID: project.ID, APIKeyID: "deleted-key", Status: responseJobStatusQueued, Phase: responseJobPhaseQueued}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillAsyncJobTenantAttribution(store.db); err != nil {
		t.Fatal(err)
	}
	var image ImageJob
	if err := store.db.First(&image, "id = ?", "img_job_tenant").Error; err != nil {
		t.Fatal(err)
	}
	if image.TenantExternalID != "tenant-job" {
		t.Fatalf("image job tenant = %q, want tenant-job", image.TenantExternalID)
	}
	var response ResponseJob
	if err := store.db.First(&response, "id = ?", "resp_job_tenant").Error; err != nil {
		t.Fatal(err)
	}
	if response.TenantExternalID != "tenant-job" {
		t.Fatalf("response job tenant = %q, want tenant-job", response.TenantExternalID)
	}
	response = ResponseJob{}
	if err := store.db.First(&response, "id = ?", "resp_job_unknown").Error; err != nil {
		t.Fatal(err)
	}
	if response.TenantExternalID != unattributedTenantExternalID {
		t.Fatalf("deleted-key response job tenant = %q, want unassigned marker", response.TenantExternalID)
	}
}

func TestAsyncJobCreationHydratesTenantAndManagedUserAttribution(t *testing.T) {
	store := NewMemoryStore()
	managedUser, err := store.CreateAdminUser(AdminUser{
		ID: "usr_async_managed", Username: "async-managed", Email: "async-managed@example.test",
		Role: "user", Status: StatusActive,
	}, "AsyncManaged123!")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&GatewayManagedUser{ExternalPrincipalID: "principal-async", AdminUserID: managedUser.ID}).Error; err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{ID: "prj_async_hydrate", Name: "Async hydrate", OwnerUserID: "usr_project_owner", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		ID: "key_async_hydrate", TenantExternalID: "tenant-async", Status: StatusActive,
		ManagedBy: gatewayModelAccessKeyManagedBy, PrincipalType: "user", PrincipalExternalID: "principal-async",
	}, "thk_async_hydrate")
	if err != nil {
		t.Fatal(err)
	}
	response, err := store.CreateResponseJob(ResponseJob{ID: "resp_async_hydrate", ProjectID: project.ID, APIKeyID: key.ID}, []byte(`{"model":"async"}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.TenantExternalID != "tenant-async" || response.AttributedUserID != managedUser.ID {
		t.Fatalf("response job attribution = tenant %q user %q", response.TenantExternalID, response.AttributedUserID)
	}
	image, err := store.CreateImageJob(ImageJob{ID: "img_async_hydrate", ProjectID: project.ID, APIKeyID: key.ID}, "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if image.TenantExternalID != "tenant-async" || image.AttributedUserID != managedUser.ID {
		t.Fatalf("image job attribution = tenant %q user %q", image.TenantExternalID, image.AttributedUserID)
	}
}

type asyncAttributionStoreDecorator struct{ Store }

func TestAsyncJobAttributionSurvivesStoreDecoration(t *testing.T) {
	store := NewMemoryStore()
	managedUser, err := store.CreateAdminUser(AdminUser{
		ID: "usr_async_decorated", Username: "async-decorated", Email: "async-decorated@example.test",
		Role: "user", Status: StatusActive,
	}, "AsyncDecorated123!")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&GatewayManagedUser{ExternalPrincipalID: "principal-decorated", AdminUserID: managedUser.ID}).Error; err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{ID: "prj_async_decorated", OwnerUserID: "usr_project_owner", Status: StatusActive})
	key := APIKey{
		ManagedBy: gatewayModelAccessKeyManagedBy, PrincipalType: "user", PrincipalExternalID: "principal-decorated",
	}
	decorated := asyncAttributionStoreDecorator{Store: store}

	if got := resolvedUsageAttributionUserID(decorated, key, project); got != managedUser.ID {
		t.Fatalf("decorated store attribution = %q, want %q", got, managedUser.ID)
	}
}

func TestAsyncJobCreationPreservesAdmissionAttribution(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_async_pinned", Name: "Pinned attribution", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_async_pinned", TenantExternalID: "tenant-pinned", Status: StatusActive}, "thk_async_pinned")
	if err != nil {
		t.Fatal(err)
	}
	pinnedUserID := "usr_pinned_at_admission"
	response, err := store.CreateResponseJob(ResponseJob{
		ID: "resp_async_pinned", ProjectID: project.ID, APIKeyID: key.ID,
		TenantExternalID: "tenant-pinned", AttributedUserID: pinnedUserID,
	}, []byte(`{"model":"async"}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.TenantExternalID != "tenant-pinned" || response.AttributedUserID != pinnedUserID {
		t.Fatalf("response job changed pinned attribution = tenant %q user %q", response.TenantExternalID, response.AttributedUserID)
	}
	image, err := store.CreateImageJob(ImageJob{
		ID: "img_async_pinned", ProjectID: project.ID, APIKeyID: key.ID,
		TenantExternalID: "tenant-pinned", AttributedUserID: pinnedUserID,
	}, "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if image.TenantExternalID != "tenant-pinned" || image.AttributedUserID != pinnedUserID {
		t.Fatalf("image job changed pinned attribution = tenant %q user %q", image.TenantExternalID, image.AttributedUserID)
	}

	admitted := imageJobWithAdmission(ImageJob{}, CallContext{
		Key: APIKey{TenantExternalID: "tenant-pinned"}, AttributedUserID: pinnedUserID,
	})
	if admitted.TenantExternalID != "tenant-pinned" || admitted.AttributedUserID != pinnedUserID {
		t.Fatalf("image admission attribution = tenant %q user %q", admitted.TenantExternalID, admitted.AttributedUserID)
	}
}

func TestQuotaBucketForUpdateRepairsUnassignedTenantMarker(t *testing.T) {
	store := NewMemoryStore()
	if err := store.db.Create(&QuotaBucket{
		KeyID: "key_recreated", Scope: "day", Bucket: "2026-10-01",
		AttributedUserID: unattributedQuotaUserID, TenantExternalID: unattributedTenantExternalID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Transaction(func(tx *gorm.DB) error {
		_, err := store.quotaBucketForUpdateWithTenant(tx, "key_recreated", "day", "2026-10-01", "tenant-recreated")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var row QuotaBucket
	if err := store.db.First(&row, "key_id = ? AND scope = ? AND bucket = ?", "key_recreated", "day", "2026-10-01").Error; err != nil {
		t.Fatal(err)
	}
	if row.TenantExternalID != "tenant-recreated" {
		t.Fatalf("recreated key tenant marker = %q, want tenant-recreated", row.TenantExternalID)
	}
}
