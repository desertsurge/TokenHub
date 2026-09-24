package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestGatewayIntegrationDependenciesRequireProjection(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	path := "/api/internal/integration/dependencies?tenant_id=tenant_01&project_id=project_01&principal_type=user&principal_id=user_01"

	unauthorized := doJSON(t, app, http.MethodGet, path, nil, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("dependency read did not require integration token: %d", unauthorized.Code)
	}
	pending := doJSON(t, app, http.MethodGet, path, nil, "integration_token")
	if pending.Code != http.StatusConflict || !strings.Contains(pending.Body, `"dependency_code":"gateway_tenant_unavailable"`) {
		t.Fatalf("missing tenant projection was not reported: status=%d body=%s", pending.Code, pending.Body)
	}
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_ready_tenant", "tenant.created", "tenant", "tenant_01", "tenant_01", 1, map[string]interface{}{
			"externalId": "tenant_01", "name": "Tenant", "status": "active",
		}),
		gatewayIntegrationEvent("evt_ready_member", "tenant_member.added", "tenant_member", "member_01", "tenant_01", 1, map[string]interface{}{
			"externalId": "member_01", "principalExternalId": "user_01", "name": "User", "email": "user@example.test", "status": "active",
		}),
		gatewayIntegrationEvent("evt_ready_project", "project.created", "project", "project_01", "tenant_01", 1, map[string]interface{}{
			"externalId": "project_01", "name": "Project", "status": "active", "ownerExternalId": "user_01",
		}),
	} {
		response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
		if response.Code != http.StatusOK {
			t.Fatalf("seed projection failed: status=%d body=%s", response.Code, response.Body)
		}
	}
	ready := doJSON(t, app, http.MethodGet, path, nil, "integration_token")
	if ready.Code != http.StatusOK || !strings.Contains(ready.Body, `"ready":true`) {
		t.Fatalf("ready projection was rejected: status=%d body=%s", ready.Code, ready.Body)
	}
}
