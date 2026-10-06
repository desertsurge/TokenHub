package server

import (
	"net/http"
	"testing"
	"time"
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
	if err := store.db.Model(&Project{}).Where("id = ?", projection.ID).Updates(map[string]interface{}{
		"owner_user_id": "stale_owner",
		"status":        StatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_owner_member_removed_replay", "tenant_member.removed", "tenant_member", "membership_owner", "tenant_owner", 3, map[string]interface{}{
		"externalId": "membership_owner", "principalExternalId": "principal_owner", "status": "removed",
	}))
	assertServingOwner(StatusDisabled, false)

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_owner_member_restored", "tenant_member.updated", "tenant_member", "membership_owner", "tenant_owner", 4, map[string]interface{}{
		"externalId": "membership_owner", "principalExternalId": "principal_owner", "name": "Project owner", "email": "owner@example.com", "status": StatusActive,
	}))
	assertServingOwner(StatusActive, true)
}

func TestGatewayPrincipalWithoutManagedUserRefreshesLegacyOwnedProject(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_legacy_owner_tenant", "tenant.created", "tenant", "tenant_legacy_owner", "tenant_legacy_owner", 1, map[string]interface{}{
		"externalId": "tenant_legacy_owner", "name": "Legacy owner tenant",
	}))
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_legacy_owner_member", "tenant_member.added", "tenant_member", "membership_legacy_owner", "tenant_legacy_owner", 1, map[string]interface{}{
		"externalId": "membership_legacy_owner", "principalExternalId": "principal_legacy_owner", "name": "Legacy project owner",
	}))

	var principal GatewayPrincipal
	if err := store.db.First(&principal, "external_principal_id = ?", "principal_legacy_owner").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	projection := GatewayProject{
		ID: "gwj_legacy_owner", TenantID: principal.TenantID, ExternalProjectID: "project_legacy_owner",
		OwnerPrincipalID: principal.ID, Name: "Legacy owned project", Status: StatusActive, Version: 1, SyncedAt: now,
	}
	if err := store.db.Create(&projection).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&Project{
		ID: projection.ID, Name: projection.Name, OwnerUserID: "stale_owner", Status: StatusActive,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_legacy_owner_removed", "tenant_member.removed", "tenant_member", "membership_legacy_owner", "tenant_legacy_owner", 2, map[string]interface{}{
		"externalId": "membership_legacy_owner", "principalExternalId": "principal_legacy_owner", "status": "removed",
	}))
	project, found := store.GetProject(projection.ID)
	if !found || project.Status != StatusDisabled || project.OwnerUserID != "" {
		t.Fatalf("legacy owned project was not disabled with its unavailable owner: found=%v project=%+v", found, project)
	}
}

func TestGatewayProjectDeletionAcceptsInactiveManagedOwner(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_delete_owner_tenant", "tenant.created", "tenant", "tenant_delete_owner", "tenant_delete_owner", 1, map[string]interface{}{
			"externalId": "tenant_delete_owner", "name": "Delete owner tenant",
		}),
		gatewayIntegrationEvent("evt_delete_owner_member", "tenant_member.added", "tenant_member", "membership_delete_owner", "tenant_delete_owner", 1, map[string]interface{}{
			"externalId": "membership_delete_owner", "principalExternalId": "principal_delete_owner", "name": "Delete owner", "email": "delete-owner@example.com",
		}),
		gatewayIntegrationEvent("evt_delete_owner_cost_center", "cost_center.created", "cost_center", "cost_center_delete_owner", "tenant_delete_owner", 1, map[string]interface{}{
			"externalId": "cost_center_delete_owner", "name": "Delete owner cost center", "code": "DELETE-OWNER",
		}),
		gatewayIntegrationEvent("evt_delete_owner_project", "project.created", "project", "project_delete_owner", "tenant_delete_owner", 1, map[string]interface{}{
			"externalId": "project_delete_owner", "name": "Delete owner project", "ownerExternalId": "principal_delete_owner", "costCenterExternalId": "cost_center_delete_owner",
		}),
		gatewayIntegrationEvent("evt_delete_owner_removed", "tenant_member.removed", "tenant_member", "membership_delete_owner", "tenant_delete_owner", 2, map[string]interface{}{
			"externalId": "membership_delete_owner", "principalExternalId": "principal_delete_owner", "status": "removed",
		}),
		gatewayIntegrationEvent("evt_delete_owner_cost_center_removed", "cost_center.deleted", "cost_center", "cost_center_delete_owner", "tenant_delete_owner", 2, map[string]interface{}{
			"externalId": "cost_center_delete_owner", "code": "DELETE-OWNER", "status": integrationStatusDeleted,
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}

	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", gatewayIntegrationEvent("evt_delete_owner_project_deleted", "project.deleted", "project", "project_delete_owner", "tenant_delete_owner", 2, map[string]interface{}{
		"externalId": "project_delete_owner", "ownerExternalId": "principal_delete_owner", "costCenterExternalId": "cost_center_delete_owner", "status": integrationStatusDeleted,
	}), "integration_token")
	if response.Code != http.StatusOK {
		t.Fatalf("expected project deletion with inactive owner to succeed, got %d: %s", response.Code, response.Body)
	}
	var projection GatewayProject
	if err := store.db.First(&projection, "external_project_id = ?", "project_delete_owner").Error; err != nil {
		t.Fatal(err)
	}
	if projection.Status != integrationStatusDeleted || projection.DeletedAt == nil {
		t.Fatalf("project deletion was not projected: %+v", projection)
	}
}
