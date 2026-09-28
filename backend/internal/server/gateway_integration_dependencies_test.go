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
	principalPath := "/api/internal/integration/dependencies?tenant_id=tenant_01&principal_type=user&principal_id=user_01"

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
	principalReady := doJSON(t, app, http.MethodGet, principalPath, nil, "integration_token")
	if principalReady.Code != http.StatusOK || !strings.Contains(principalReady.Body, `"ready":true`) {
		t.Fatalf("ready principal was rejected without a project: status=%d body=%s", principalReady.Code, principalReady.Body)
	}
	stalePrincipal := doJSON(t, app, http.MethodGet, principalPath+"&tenant_version=2&principal_version=2", nil, "integration_token")
	if stalePrincipal.Code != http.StatusConflict || !strings.Contains(stalePrincipal.Body, `"dependency_code":"gateway_tenant_stale"`) {
		t.Fatalf("stale tenant projection was reported ready: status=%d body=%s", stalePrincipal.Code, stalePrincipal.Body)
	}
	tenantUpdate := gatewayIntegrationEvent("evt_ready_tenant_v2", "tenant.updated", "tenant", "tenant_01", "tenant_01", 2, map[string]interface{}{
		"externalId": "tenant_01", "name": "Tenant", "status": "active",
	})
	if response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", tenantUpdate, "integration_token"); response.Code != http.StatusOK {
		t.Fatalf("tenant projection update failed: status=%d body=%s", response.Code, response.Body)
	}
	stalePrincipal = doJSON(t, app, http.MethodGet, principalPath+"&tenant_version=2&principal_version=2", nil, "integration_token")
	if stalePrincipal.Code != http.StatusConflict || !strings.Contains(stalePrincipal.Body, `"dependency_code":"gateway_principal_stale"`) {
		t.Fatalf("stale principal projection was reported ready: status=%d body=%s", stalePrincipal.Code, stalePrincipal.Body)
	}
	memberUpdate := gatewayIntegrationEvent("evt_ready_member_v2", "tenant_member.updated", "tenant_member", "member_01", "tenant_01", 2, map[string]interface{}{
		"externalId": "member_01", "principalExternalId": "user_01", "name": "Updated User", "email": "updated@example.test", "status": "active",
	})
	if response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", memberUpdate, "integration_token"); response.Code != http.StatusOK {
		t.Fatalf("principal projection update failed: status=%d body=%s", response.Code, response.Body)
	}
	versionReady := doJSON(t, app, http.MethodGet, principalPath+"&tenant_version=2&principal_version=2", nil, "integration_token")
	if versionReady.Code != http.StatusOK || !strings.Contains(versionReady.Body, `"ready":true`) ||
		!strings.Contains(versionReady.Body, `"tenant_version":2`) || !strings.Contains(versionReady.Body, `"principal_version":2`) {
		t.Fatalf("current projection version was rejected: status=%d body=%s", versionReady.Code, versionReady.Body)
	}
	invalidVersion := doJSON(t, app, http.MethodGet, principalPath+"&principal_version=0", nil, "integration_token")
	if invalidVersion.Code != http.StatusBadRequest {
		t.Fatalf("invalid projection version was accepted: status=%d body=%s", invalidVersion.Code, invalidVersion.Body)
	}

	missingProject := doJSON(t, app, http.MethodGet, "/api/internal/integration/dependencies?tenant_id=tenant_01&principal_type=service_account&principal_id=service_01", nil, "integration_token")
	if missingProject.Code != http.StatusBadRequest {
		t.Fatalf("workload readiness accepted a missing project: status=%d body=%s", missingProject.Code, missingProject.Body)
	}
	withoutEmail := gatewayIntegrationEvent("evt_ready_member_no_email", "tenant_member.added", "tenant_member", "member_02", "tenant_01", 1, map[string]interface{}{
		"externalId": "member_02", "principalExternalId": "user_02", "name": "No Email", "status": "active",
	})
	if response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", withoutEmail, "integration_token"); response.Code != http.StatusOK {
		t.Fatalf("seed principal without managed account failed: status=%d body=%s", response.Code, response.Body)
	}
	for _, dependencyPath := range []string{
		"/api/internal/integration/dependencies?tenant_id=tenant_01&principal_type=user&principal_id=user_02",
		"/api/internal/integration/dependencies?tenant_id=tenant_01&project_id=project_01&principal_type=user&principal_id=user_02",
	} {
		pendingUser := doJSON(t, app, http.MethodGet, dependencyPath, nil, "integration_token")
		if pendingUser.Code != http.StatusConflict || !strings.Contains(pendingUser.Body, `"dependency_code":"gateway_user_unavailable"`) {
			t.Fatalf("principal without a managed account was reported ready: status=%d body=%s", pendingUser.Code, pendingUser.Body)
		}
	}
}
