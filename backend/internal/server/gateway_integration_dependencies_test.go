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
	unsupported := doJSON(t, app, http.MethodGet, "/api/internal/integration/dependencies?tenant_id=tenant_01&project_id=project_01&principal_type=robot&principal_id=robot_01", nil, "integration_token")
	if unsupported.Code != http.StatusBadRequest || !strings.Contains(unsupported.Body, "invalid_integration_dependency_query") {
		t.Fatalf("unsupported principal type was accepted: status=%d body=%s", unsupported.Code, unsupported.Body)
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
	staleProject := doJSON(t, app, http.MethodGet, path+"&project_version=2", nil, "integration_token")
	if staleProject.Code != http.StatusConflict || !strings.Contains(staleProject.Body, `"dependency_code":"gateway_project_stale"`) {
		t.Fatalf("stale project projection was reported ready: status=%d body=%s", staleProject.Code, staleProject.Body)
	}
	projectUpdate := gatewayIntegrationEvent("evt_ready_project_v2", "project.updated", "project", "project_01", "tenant_01", 2, map[string]interface{}{
		"externalId": "project_01", "name": "Project", "status": "active", "ownerExternalId": "user_01",
	})
	if response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", projectUpdate, "integration_token"); response.Code != http.StatusOK {
		t.Fatalf("project projection update failed: status=%d body=%s", response.Code, response.Body)
	}
	projectReady := doJSON(t, app, http.MethodGet, path+"&project_version=2", nil, "integration_token")
	if projectReady.Code != http.StatusOK || !strings.Contains(projectReady.Body, `"project_version":2`) {
		t.Fatalf("current project projection version was rejected: status=%d body=%s", projectReady.Code, projectReady.Body)
	}
	principalReady := doJSON(t, app, http.MethodGet, principalPath, nil, "integration_token")
	if principalReady.Code != http.StatusOK || !strings.Contains(principalReady.Body, `"ready":true`) {
		t.Fatalf("ready principal was rejected without a project: status=%d body=%s", principalReady.Code, principalReady.Body)
	}
	uppercasePrincipalReady := doJSON(t, app, http.MethodGet, strings.Replace(principalPath, "principal_type=user", "principal_type=USER", 1), nil, "integration_token")
	if uppercasePrincipalReady.Code != http.StatusOK || !strings.Contains(uppercasePrincipalReady.Body, `"principal_type":"user"`) {
		t.Fatalf("principal type was not normalized consistently: status=%d body=%s", uppercasePrincipalReady.Code, uppercasePrincipalReady.Body)
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
	invalidProjectVersion := doJSON(t, app, http.MethodGet, principalPath+"&project_version=1", nil, "integration_token")
	if invalidProjectVersion.Code != http.StatusBadRequest {
		t.Fatalf("project version without project scope was accepted: status=%d body=%s", invalidProjectVersion.Code, invalidProjectVersion.Body)
	}
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_ready_org", "organization.created", "organization", "org_01", "tenant_01", 1, map[string]interface{}{
			"externalId": "org_01", "name": "Organization", "status": "active",
		}),
		gatewayIntegrationEvent("evt_ready_org_member", "organization_member.added", "organization_member", "org_member_01", "tenant_01", 1, map[string]interface{}{
			"externalId": "org_member_01", "principalExternalId": "user_01", "organizationExternalId": "org_01", "status": "active",
		}),
		gatewayIntegrationEvent("evt_ready_org_project", "project.created", "project", "project_org_01", "tenant_01", 1, map[string]interface{}{
			"externalId": "project_org_01", "organizationExternalId": "org_01", "name": "Organization project", "status": "active", "ownerExternalId": "user_01",
		}),
	} {
		if response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token"); response.Code != http.StatusOK {
			t.Fatalf("seed organization dependency failed: status=%d body=%s", response.Code, response.Body)
		}
	}
	organizationPath := "/api/internal/integration/dependencies?tenant_id=tenant_01&project_id=project_org_01&principal_type=user&principal_id=user_01&project_version=1&organization_membership_version=2"
	staleMembership := doJSON(t, app, http.MethodGet, organizationPath, nil, "integration_token")
	if staleMembership.Code != http.StatusConflict || !strings.Contains(staleMembership.Body, `"dependency_code":"gateway_organization_membership_stale"`) {
		t.Fatalf("stale organization membership was reported ready: status=%d body=%s", staleMembership.Code, staleMembership.Body)
	}
	bindingUpdate := gatewayIntegrationEvent("evt_ready_org_member_v2", "organization_member.updated", "organization_member", "org_member_01", "tenant_01", 2, map[string]interface{}{
		"externalId": "org_member_01", "principalExternalId": "user_01", "organizationExternalId": "org_01", "status": "active",
	})
	if response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", bindingUpdate, "integration_token"); response.Code != http.StatusOK {
		t.Fatalf("organization membership update failed: status=%d body=%s", response.Code, response.Body)
	}
	membershipReady := doJSON(t, app, http.MethodGet, organizationPath, nil, "integration_token")
	if membershipReady.Code != http.StatusOK || !strings.Contains(membershipReady.Body, `"project_version":1`) ||
		!strings.Contains(membershipReady.Body, `"organization_membership_version":2`) {
		t.Fatalf("current organization membership version was rejected: status=%d body=%s", membershipReady.Code, membershipReady.Body)
	}
	var tenant GatewayTenant
	if err := store.db.First(&tenant, "external_tenant_id = ?", "tenant_01").Error; err != nil {
		t.Fatal(err)
	}
	var principal GatewayPrincipal
	if err := store.db.First(&principal, "tenant_id = ? AND external_principal_id = ?", tenant.ID, "user_01").Error; err != nil {
		t.Fatal(err)
	}
	var organization GatewayOrganization
	if err := store.db.First(&organization, "tenant_id = ? AND external_organization_id = ?", tenant.ID, "org_01").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&GatewayPrincipalOrganizationBinding{
		ID: "aaa_legacy_membership", TenantID: tenant.ID, ExternalMembershipID: "legacy_membership",
		PrincipalID: principal.ID, OrganizationID: organization.ID, Status: StatusActive, Version: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	newestMembershipReady := doJSON(t, app, http.MethodGet, organizationPath, nil, "integration_token")
	if newestMembershipReady.Code != http.StatusOK || !strings.Contains(newestMembershipReady.Body, `"organization_membership_version":2`) {
		t.Fatalf("an older parallel membership masked the current version: status=%d body=%s", newestMembershipReady.Code, newestMembershipReady.Body)
	}

	missingProject := doJSON(t, app, http.MethodGet, "/api/internal/integration/dependencies?tenant_id=tenant_01&principal_type=service_account&principal_id=service_01", nil, "integration_token")
	if missingProject.Code != http.StatusBadRequest {
		t.Fatalf("workload readiness accepted a missing project: status=%d body=%s", missingProject.Code, missingProject.Body)
	}
	withoutEmail := gatewayIntegrationEvent("evt_ready_member_no_email", "tenant_member.added", "tenant_member", "member_02", "tenant_01", 1, map[string]interface{}{
		"externalId": "member_02", "principalExternalId": "user_02", "name": "No Email", "status": "active",
	})
	if response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", withoutEmail, "integration_token"); response.Code != http.StatusOK {
		t.Fatalf("seed principal with synthetic managed account failed: status=%d body=%s", response.Code, response.Body)
	}
	for _, dependencyPath := range []string{
		"/api/internal/integration/dependencies?tenant_id=tenant_01&principal_type=user&principal_id=user_02",
		"/api/internal/integration/dependencies?tenant_id=tenant_01&project_id=project_01&principal_type=user&principal_id=user_02",
	} {
		readyUser := doJSON(t, app, http.MethodGet, dependencyPath, nil, "integration_token")
		if readyUser.Code != http.StatusOK || !strings.Contains(readyUser.Body, `"ready":true`) {
			t.Fatalf("principal with a synthetic managed account was not ready: status=%d body=%s", readyUser.Code, readyUser.Body)
		}
	}
}

func TestGatewayIntegrationDependenciesPreserveDatabaseFailures(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	if err := store.db.Migrator().DropTable(&GatewayTenant{}); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, app, http.MethodGet, "/api/internal/integration/dependencies?tenant_id=tenant_01&principal_type=user&principal_id=user_01", nil, "integration_token")
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body, `"code":"internal_error"`) {
		t.Fatalf("database failure was misreported as a pending dependency: status=%d body=%s", response.Code, response.Body)
	}
}
