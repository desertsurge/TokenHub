package server

import (
	"encoding/json"
	"net/http"
	"strings"
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

func TestAdminUserQuotaRequiresAndValidatesTenantContext(t *testing.T) {
	store := NewMemoryStore()
	user, err := store.CreateAdminUser(AdminUser{ID: "quota-multi-tenant-user", Username: "quota-multi-tenant-user", Email: "quota-multi-tenant@example.test", Role: "user", Status: StatusActive}, "QuotaMultiTenant123!")
	if err != nil {
		t.Fatal(err)
	}
	principalID := "principal-multi-tenant"
	if err := store.db.Create(&GatewayManagedUser{ExternalPrincipalID: principalID, AdminUserID: user.ID}).Error; err != nil {
		t.Fatal(err)
	}
	for index, tenantID := range []string{"tenant-a", "tenant-b"} {
		tenant := GatewayTenant{ID: "gt-" + tenantID, ExternalTenantID: tenantID, Name: "Tenant " + tenantID, Status: StatusActive}
		if err := store.db.Create(&tenant).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&GatewayPrincipal{ID: "gp-" + tenantID, TenantID: tenant.ID, ExternalPrincipalID: principalID, Status: StatusActive}).Error; err != nil {
			t.Fatal(err)
		}
		store.CreateResource("quota-policies", AdminResource{ID: "quota-" + tenantID, Name: tenantID, Status: StatusActive, Fields: map[string]any{
			"scope": "user", "scope_id": user.ID, "tenant_external_id": tenantID, "daily_tokens": int64(100),
		}})
		if err := store.db.Create(&QuotaBucket{KeyID: "key-" + tenantID, Scope: "day", Bucket: dayBucket(time.Now().UTC()), AttributedUserID: user.ID, TenantExternalID: tenantID, QuotaCounter: QuotaCounter{TotalTokens: int64(index + 1)}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	store.CreateResource("quota-policies", AdminResource{ID: "quota-unscoped", Name: "Legacy unscoped user quota", Status: StatusActive, Fields: map[string]any{
		"scope": "user", "scope_id": user.ID, "daily_tokens": int64(1),
	}})
	_, session, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	contexts := doJSON(t, app, http.MethodGet, "/api/admin/usage/quota/tenants", nil, session.Token)
	if contexts.Code != http.StatusOK {
		t.Fatalf("quota tenant contexts = %d %s", contexts.Code, contexts.Body)
	}
	var contextPayload struct {
		Data []UserQuotaTenant `json:"data"`
	}
	if err := json.Unmarshal([]byte(contexts.Body), &contextPayload); err != nil {
		t.Fatal(err)
	}
	if len(contextPayload.Data) != 2 || contextPayload.Data[0].ID != "tenant-a" || contextPayload.Data[1].ID != "tenant-b" {
		t.Fatalf("quota tenant contexts = %+v", contextPayload.Data)
	}
	ambiguous := doJSON(t, app, http.MethodGet, "/api/admin/usage/quota", nil, session.Token)
	if ambiguous.Code != http.StatusConflict || !containsJSONCode(ambiguous.Body, "tenant_context_required") {
		t.Fatalf("multi-tenant quota request = %d %s", ambiguous.Code, ambiguous.Body)
	}
	selected := doJSON(t, app, http.MethodGet, "/api/admin/usage/quota?tenant_id=tenant-a", nil, session.Token)
	if selected.Code != http.StatusOK {
		t.Fatalf("tenant-scoped quota request = %d %s", selected.Code, selected.Body)
	}
	var snapshot UserQuotaSnapshot
	if err := json.Unmarshal([]byte(selected.Body), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.TenantID != "tenant-a" || snapshot.Usage.Daily.TotalTokens != 1 || snapshot.Limits.DailyTokens != 100 {
		t.Fatalf("tenant-a quota snapshot = %+v", snapshot)
	}
	forbidden := doJSON(t, app, http.MethodGet, "/api/admin/usage/quota?tenant_id=tenant-c", nil, session.Token)
	if forbidden.Code != http.StatusForbidden || !containsJSONCode(forbidden.Body, "tenant_context_forbidden") {
		t.Fatalf("foreign tenant quota request = %d %s", forbidden.Code, forbidden.Body)
	}
}

func TestTeamLeaderCannotManageUserQuotaOutsideSharedTenant(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_quota", Name: "Quota team", Status: StatusActive})
	leader, err := store.CreateAdminUser(AdminUser{ID: "usr_quota_leader", Username: "quota-leader", Name: "Quota Leader", Email: "quota-leader@example.test", Role: "team_leader", TeamID: "team_quota", Status: StatusActive}, "QuotaLeader123456!")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateAdminUser(AdminUser{ID: "usr_quota_target", Username: "quota-target", Name: "Quota Target", Email: "quota-target@example.test", Role: "user", TeamID: "team_quota", Status: StatusActive}, "QuotaTarget123456!")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		tenantID  string
		principal string
		adminUser string
	}{
		{tenantID: "tenant-shared", principal: "principal-quota-leader", adminUser: leader.ID},
		{tenantID: "tenant-shared", principal: "principal-quota-target", adminUser: target.ID},
	} {
		if err := store.db.FirstOrCreate(&GatewayTenant{ID: "gt-" + item.tenantID, ExternalTenantID: item.tenantID, Name: item.tenantID, Status: StatusActive}).Error; err != nil {
			t.Fatal(err)
		}
		var tenant GatewayTenant
		if err := store.db.First(&tenant, "external_tenant_id = ?", item.tenantID).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&GatewayPrincipal{ID: item.principal, TenantID: tenant.ID, ExternalPrincipalID: item.principal, Status: StatusActive}).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&GatewayManagedUser{ExternalPrincipalID: item.principal, AdminUserID: item.adminUser}).Error; err != nil {
			t.Fatal(err)
		}
	}
	store.CreateResource("quota-policies", AdminResource{ID: "quota-shared-tenant", Name: "Shared tenant quota", Status: StatusActive, Fields: map[string]any{
		"scope": "user", "scope_id": target.ID, "tenant_external_id": "tenant-shared", "daily_tokens": int64(100),
	}})
	store.CreateResource("quota-policies", AdminResource{ID: "quota-foreign-tenant", Name: "Foreign tenant quota", Status: StatusActive, Fields: map[string]any{
		"scope": "user", "scope_id": target.ID, "tenant_external_id": "tenant-foreign", "daily_tokens": int64(100),
	}})
	_, session, err := store.CreateAdminSession(leader.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !userHasTeam(target, leader.TeamID) {
		t.Fatalf("quota target is not in the leader's team: leader=%+v target=%+v", leader, target)
	}
	if tenants, err := store.ListAdminQuotaTenants(leader.ID); err != nil || len(tenants) != 1 || tenants[0].ID != "tenant-shared" {
		t.Fatalf("leader tenant fixture = %+v, err=%v", tenants, err)
	}
	if tenants, err := store.ListAdminQuotaTenants(target.ID); err != nil || len(tenants) != 1 || tenants[0].ID != "tenant-shared" {
		t.Fatalf("target tenant fixture = %+v, err=%v", tenants, err)
	}
	if resources := store.ListResources("quota-policies"); len(resources) != 2 {
		t.Fatalf("quota fixtures = %+v", resources)
	}
	sessionUser, ok := store.ValidateAdminSession(session.Token)
	if !ok || sessionUser.TeamID != "team_quota" {
		t.Fatalf("leader session team = %+v, valid=%v", sessionUser, ok)
	}
	server := New(store)
	storedTarget, found := server.findAdminUser(target.ID)
	if !found || !userHasTeam(storedTarget, sessionUser.TeamID) {
		t.Fatalf("stored target team = %+v, found=%v", storedTarget, found)
	}
	actorSet, actorErr := server.adminUserTenantSet(sessionUser.ID)
	targetSet, targetErr := server.adminUserTenantSet(storedTarget.ID)
	if actorErr != nil || targetErr != nil || !actorSet["tenant-shared"] || !targetSet["tenant-shared"] {
		t.Fatalf("quota tenant sets: actor=%+v (%v), target=%+v (%v)", actorSet, actorErr, targetSet, targetErr)
	}
	if filtered := server.filterQuotaPoliciesForTeamLeader(sessionUser, store.ListResources("quota-policies")); len(filtered) != 1 {
		t.Fatalf("direct quota visibility = %+v, actor=%+v", filtered, sessionUser)
	}
	app := server.Handler()
	listed := doJSON(t, app, http.MethodGet, "/api/admin/resources/quota-policies", nil, session.Token)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body, "quota-shared-tenant") || strings.Contains(listed.Body, "quota-foreign-tenant") {
		t.Fatalf("team leader quota listing crossed tenant boundary: %d %s", listed.Code, listed.Body)
	}
	updated := doJSON(t, app, http.MethodPatch, "/api/admin/resources/quota-policies/quota-foreign-tenant", map[string]any{
		"fields": map[string]any{"daily_tokens": int64(50)},
	}, session.Token)
	if updated.Code != http.StatusForbidden || !strings.Contains(updated.Body, "quota_forbidden") {
		t.Fatalf("team leader modified foreign-tenant quota: %d %s", updated.Code, updated.Body)
	}
	created := doJSON(t, app, http.MethodPost, "/api/admin/resources/quota-policies", map[string]any{
		"name": "Invalid foreign tenant quota", "fields": map[string]any{
			"scope": "user", "scope_id": target.ID, "tenant_external_id": "tenant-foreign", "daily_tokens": int64(10),
		},
	}, session.Token)
	if created.Code != http.StatusForbidden || !strings.Contains(created.Body, "quota_forbidden") {
		t.Fatalf("team leader created foreign-tenant quota: %d %s", created.Code, created.Body)
	}
	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/resources/quota-policies/quota-foreign-tenant", nil, session.Token)
	if deleted.Code != http.StatusForbidden || !strings.Contains(deleted.Body, "quota_forbidden") {
		t.Fatalf("team leader deleted foreign-tenant quota: %d %s", deleted.Code, deleted.Body)
	}
	for _, item := range store.ListResources("quota-policies") {
		if item.ID == "quota-foreign-tenant" {
			return
		}
	}
	t.Fatal("foreign-tenant quota was removed despite authorization failure")
}
