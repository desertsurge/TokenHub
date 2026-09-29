package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGatewayUserQuotaIsTenantScopedAndUsesManagedUserPolicy(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)

	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "user_01").Error; err != nil {
		t.Fatal(err)
	}
	store.CreateResource("quota-policies", AdminResource{
		ID:     "quota_user_01",
		Name:   "User quota",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":            "user",
			"scope_id":         managed.AdminUserID,
			"daily_tokens":     1000,
			"monthly_tokens":   5000,
			"daily_requests":   10,
			"monthly_requests": 100,
		},
	})

	quota := doJSON(t, app, http.MethodGet, "/api/internal/user-quota?tenant_id=tenant_01&principal_id=user_01", nil, "integration_token")
	if quota.Code != http.StatusOK {
		t.Fatalf("expected self quota, got %d: %s", quota.Code, quota.Body)
	}
	var payload struct {
		Data UserQuotaSnapshot `json:"data"`
	}
	if err := json.Unmarshal([]byte(quota.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Data.PolicyConfigured || payload.Data.Limits.DailyTokens != 1000 || payload.Data.Limits.MonthlyTokens != 5000 {
		t.Fatalf("unexpected self quota: %+v", payload.Data)
	}
	if payload.Data.UserID != "" {
		t.Fatalf("internal user id must not be exposed: %q", payload.Data.UserID)
	}

	otherTenant := doJSON(t, app, http.MethodGet, "/api/internal/user-quota?tenant_id=tenant_02&principal_id=user_01", nil, "integration_token")
	if otherTenant.Code != http.StatusForbidden || !jsonBodyHasCode(otherTenant.Body, "gateway_membership_inactive") {
		t.Fatalf("expected cross-tenant membership rejection, got %d: %s", otherTenant.Code, otherTenant.Body)
	}
	missingPrincipal := doJSON(t, app, http.MethodGet, "/api/internal/user-quota?tenant_id=tenant_01&principal_id=user_missing", nil, "integration_token")
	if missingPrincipal.Code != http.StatusForbidden || !jsonBodyHasCode(missingPrincipal.Body, "gateway_membership_inactive") {
		t.Fatalf("expected missing membership rejection, got %d: %s", missingPrincipal.Code, missingPrincipal.Body)
	}
}
