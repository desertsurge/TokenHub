package server

import (
	"net/http"
	"testing"
)

func TestGatewayProjectRequiresActiveManagedOwner(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_owner_tenant", "tenant.created", "tenant", "tenant_owner", "tenant_owner", 1, map[string]interface{}{
		"externalId": "tenant_owner", "name": "Owner tenant",
	}))
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_owner_member_without_email", "tenant_member.added", "tenant_member", "membership_owner", "tenant_owner", 1, map[string]interface{}{
		"externalId": "membership_owner", "principalExternalId": "principal_owner", "name": "Project owner",
	}))

	projectEvent := gatewayIntegrationEvent("evt_owner_project", "project.created", "project", "project_owner", "tenant_owner", 1, map[string]interface{}{
		"externalId": "project_owner", "name": "Owned project", "ownerExternalId": "principal_owner",
	})
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", projectEvent, "integration_token")
	if response.Code != http.StatusConflict || !jsonBodyHasCode(response.Body, "integration_dependency_missing") {
		t.Fatalf("expected unsynchronized owner rejection, got %d: %s", response.Code, response.Body)
	}
	var projectCount int64
	if err := store.db.Model(&GatewayProject{}).Where("external_project_id = ?", "project_owner").Count(&projectCount).Error; err != nil {
		t.Fatal(err)
	}
	if projectCount != 0 {
		t.Fatalf("rejected project event persisted %d projections", projectCount)
	}

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_owner_member_ready", "tenant_member.updated", "tenant_member", "membership_owner", "tenant_owner", 2, map[string]interface{}{
		"externalId": "membership_owner", "principalExternalId": "principal_owner", "name": "Project owner", "email": "owner@example.com", "status": StatusActive,
	}))
	applyGatewayIntegrationEventForTest(t, app, projectEvent)

	var projection GatewayProject
	if err := store.db.First(&projection, "external_project_id = ?", "project_owner").Error; err != nil {
		t.Fatal(err)
	}
	assertServingOwner := func(wantStatus string, wantOwner bool) {
		t.Helper()
		project, found := store.GetProject(projection.ID)
		if !found || project.Status != wantStatus || (project.OwnerUserID != "") != wantOwner {
			t.Fatalf("unexpected serving project owner state: found=%v project=%+v", found, project)
		}
	}
	assertServingOwner(StatusActive, true)

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_owner_member_removed", "tenant_member.removed", "tenant_member", "membership_owner", "tenant_owner", 3, map[string]interface{}{
		"externalId": "membership_owner", "principalExternalId": "principal_owner", "status": "removed",
	}))
	assertServingOwner(StatusDisabled, false)

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_owner_member_restored", "tenant_member.updated", "tenant_member", "membership_owner", "tenant_owner", 4, map[string]interface{}{
		"externalId": "membership_owner", "principalExternalId": "principal_owner", "name": "Project owner", "email": "owner@example.com", "status": StatusActive,
	}))
	assertServingOwner(StatusActive, true)
}
