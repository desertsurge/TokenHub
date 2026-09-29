package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestAdminUserQuotaReturnsOnlyAuthenticatedUsersQuota(t *testing.T) {
	store := NewMemoryStore()
	userA, err := store.CreateAdminUser(AdminUser{ID: "usr_quota_a", Username: "quota-a", Name: "Quota A", Email: "quota-a@example.test", Role: "user", Status: StatusActive}, "QuotaA123456!")
	if err != nil {
		t.Fatal(err)
	}
	userB, err := store.CreateAdminUser(AdminUser{ID: "usr_quota_b", Username: "quota-b", Name: "Quota B", Email: "quota-b@example.test", Role: "user", Status: StatusActive}, "QuotaB123456!")
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("quota-policies", AdminResource{
		ID:     "quota_user_a",
		Name:   "Quota A policy",
		Status: StatusActive,
		Fields: map[string]any{"scope": "user", "scope_id": userA.ID, "token_limit_tpm": int64(300), "daily_tokens": int64(1000), "monthly_tokens": int64(5000)},
	})
	now := time.Now().UTC()
	if err := store.db.Create(&QuotaBucket{
		KeyID: userQuotaBucketKey(userA.ID), Scope: "day", Bucket: dayBucket(now), AttributedUserID: userA.ID,
		QuotaCounter: QuotaCounter{Requests: 2, TotalTokens: 120},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&QuotaBucket{
		KeyID: userQuotaBucketKey(userA.ID), Scope: "minute", Bucket: minuteBucket(now), AttributedUserID: userA.ID,
		QuotaCounter: QuotaCounter{Requests: 1, TotalTokens: 40},
	}).Error; err != nil {
		t.Fatal(err)
	}
	_, sessionA, err := store.CreateAdminSession(userA.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, sessionB, err := store.CreateAdminSession(userB.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	responseA := doJSON(t, app, http.MethodGet, "/api/admin/usage/quota", nil, sessionA.Token)
	if responseA.Code != http.StatusOK {
		t.Fatalf("user A quota status = %d: %s", responseA.Code, responseA.Body)
	}
	var snapshotA UserQuotaSnapshot
	if err := json.Unmarshal([]byte(responseA.Body), &snapshotA); err != nil {
		t.Fatal(err)
	}
	if snapshotA.UserID != userA.ID || !snapshotA.PolicyConfigured || snapshotA.Limits.TokenLimitTPM != 300 || snapshotA.Limits.DailyTokens != 1000 || snapshotA.Usage.Minute.TotalTokens != 40 || snapshotA.Usage.Daily.TotalTokens != 120 {
		t.Fatalf("user A quota snapshot = %+v", snapshotA)
	}

	responseB := doJSON(t, app, http.MethodGet, "/api/admin/usage/quota", nil, sessionB.Token)
	if responseB.Code != http.StatusOK {
		t.Fatalf("user B quota status = %d: %s", responseB.Code, responseB.Body)
	}
	var snapshotB UserQuotaSnapshot
	if err := json.Unmarshal([]byte(responseB.Body), &snapshotB); err != nil {
		t.Fatal(err)
	}
	if snapshotB.UserID != userB.ID || snapshotB.PolicyConfigured || snapshotB.Limits.DailyTokens != 0 || snapshotB.Usage.Daily.TotalTokens != 0 {
		t.Fatalf("user B must not see user A quota = %+v", snapshotB)
	}
}

func TestQuotaPolicyUsageKeepsTenantScope(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{ID: "quota-scope-admin", Username: "quota-scope-admin", Email: "quota-scope-admin@example.test", Role: "admin", Status: StatusActive}, "QuotaScopeAdmin123!"); err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateAdminUser(AdminUser{ID: "quota-scope-user", Username: "quota-scope-user", Email: "quota-scope-user@example.test", Role: "user", Status: StatusActive}, "QuotaScopeUser123!")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, item := range []struct {
		id     string
		tenant string
		value  int64
	}{
		{id: "quota-scope-a", tenant: "tenant-a", value: 3},
		{id: "quota-scope-b", tenant: "tenant-b", value: 9},
	} {
		store.CreateResource("quota-policies", AdminResource{
			ID: item.id, Name: item.id, Status: StatusActive,
			Fields: map[string]any{"scope": "user", "scope_id": user.ID, "tenant_external_id": item.tenant, "daily_tokens": int64(100)},
		})
		if err := store.db.Create(&QuotaBucket{
			KeyID: "key-" + item.tenant, Scope: "day", Bucket: dayBucket(now), AttributedUserID: user.ID, TenantExternalID: item.tenant,
			QuotaCounter: QuotaCounter{TotalTokens: item.value},
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	response := doJSON(t, New(store).Handler(), http.MethodGet, "/api/admin/resources/quota-policies", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("list tenant user quota policies: %d %s", response.Code, response.Body)
	}
	var payload struct {
		Data []AdminResource `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		id    string
		value int64
	}{
		{id: "quota-scope-a", value: 3},
		{id: "quota-scope-b", value: 9},
	} {
		var found *AdminResource
		for index := range payload.Data {
			if payload.Data[index].ID == want.id {
				found = &payload.Data[index]
				break
			}
		}
		if found == nil || found.CurrentUsage == nil || found.CurrentUsage.Daily.TotalTokens != want.value {
			t.Fatalf("tenant-scoped usage for %s = %+v, want %d", want.id, found, want.value)
		}
	}
}
