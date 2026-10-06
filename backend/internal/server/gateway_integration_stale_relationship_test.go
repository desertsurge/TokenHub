package server

import (
	"net/http"
	"testing"
)

func TestGatewayRelationshipStaleReplayUsesPersistedDependencies(t *testing.T) {
	t.Run("organization membership", func(t *testing.T) {
		store := NewMemoryStore()
		app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
		for _, event := range []map[string]interface{}{
			gatewayIntegrationEvent("evt_stale_binding_tenant", "tenant.created", "tenant", "tenant_stale_binding", "tenant_stale_binding", 1, map[string]interface{}{"externalId": "tenant_stale_binding", "name": "Stale binding tenant"}),
			gatewayIntegrationEvent("evt_stale_binding_principal_old", "tenant_member.added", "tenant_member", "member_old", "tenant_stale_binding", 1, map[string]interface{}{"externalId": "member_old", "principalExternalId": "principal_old", "name": "Old principal", "email": "old-principal@example.test"}),
			gatewayIntegrationEvent("evt_stale_binding_principal_current", "tenant_member.added", "tenant_member", "member_current", "tenant_stale_binding", 1, map[string]interface{}{"externalId": "member_current", "principalExternalId": "principal_current", "name": "Current principal", "email": "current-principal@example.test"}),
			gatewayIntegrationEvent("evt_stale_binding_org_old", "organization.created", "organization", "org_old", "tenant_stale_binding", 1, map[string]interface{}{"externalId": "org_old", "name": "Old organization"}),
			gatewayIntegrationEvent("evt_stale_binding_org_current", "organization.created", "organization", "org_current", "tenant_stale_binding", 1, map[string]interface{}{"externalId": "org_current", "name": "Current organization"}),
			gatewayIntegrationEvent("evt_stale_binding_v1", "organization_member.added", "organization_member", "binding_stale", "tenant_stale_binding", 1, map[string]interface{}{"externalId": "binding_stale", "principalExternalId": "principal_old", "organizationExternalId": "org_old"}),
			gatewayIntegrationEvent("evt_stale_binding_v2", "organization_member.updated", "organization_member", "binding_stale", "tenant_stale_binding", 2, map[string]interface{}{"externalId": "binding_stale", "principalExternalId": "principal_current", "organizationExternalId": "org_current"}),
		} {
			applyGatewayIntegrationEventForTest(t, app, event)
		}
		if err := store.db.Unscoped().Where("external_principal_id = ?", "principal_old").Delete(&GatewayPrincipal{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Unscoped().Where("external_organization_id = ?", "org_old").Delete(&GatewayOrganization{}).Error; err != nil {
			t.Fatal(err)
		}

		response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", gatewayIntegrationEvent("evt_stale_binding_replay", "organization_member.added", "organization_member", "binding_stale", "tenant_stale_binding", 1, map[string]interface{}{"externalId": "binding_stale", "principalExternalId": "principal_old", "organizationExternalId": "org_old"}), "integration_token")
		if response.Code != http.StatusOK || !jsonBodyHasField(response.Body, "outcome", "ignored_stale") {
			t.Fatalf("stale organization membership replay = %d %s", response.Code, response.Body)
		}
		var binding GatewayPrincipalOrganizationBinding
		if err := store.db.First(&binding, "external_membership_id = ?", "binding_stale").Error; err != nil {
			t.Fatal(err)
		}
		var principal GatewayPrincipal
		if err := store.db.First(&principal, "id = ?", binding.PrincipalID).Error; err != nil {
			t.Fatal(err)
		}
		if principal.ExternalPrincipalID != "principal_current" || binding.Version != 2 {
			t.Fatalf("stale replay changed current membership: binding=%+v principal=%+v", binding, principal)
		}
	})

	t.Run("organization cost center", func(t *testing.T) {
		store := NewMemoryStore()
		app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
		for _, event := range []map[string]interface{}{
			gatewayIntegrationEvent("evt_stale_cost_tenant", "tenant.created", "tenant", "tenant_stale_cost", "tenant_stale_cost", 1, map[string]interface{}{"externalId": "tenant_stale_cost", "name": "Stale cost tenant"}),
			gatewayIntegrationEvent("evt_stale_cost_org_old", "organization.created", "organization", "org_cost_old", "tenant_stale_cost", 1, map[string]interface{}{"externalId": "org_cost_old", "name": "Old cost organization"}),
			gatewayIntegrationEvent("evt_stale_cost_org_current", "organization.created", "organization", "org_cost_current", "tenant_stale_cost", 1, map[string]interface{}{"externalId": "org_cost_current", "name": "Current cost organization"}),
			gatewayIntegrationEvent("evt_stale_cost_center_old", "cost_center.created", "cost_center", "cc_old", "tenant_stale_cost", 1, map[string]interface{}{"externalId": "cc_old", "code": "CC-OLD", "name": "Old cost center"}),
			gatewayIntegrationEvent("evt_stale_cost_center_current", "cost_center.created", "cost_center", "cc_current", "tenant_stale_cost", 1, map[string]interface{}{"externalId": "cc_current", "code": "CC-CURRENT", "name": "Current cost center"}),
			gatewayIntegrationEvent("evt_stale_cost_binding_v1", "organization_cost_center.created", "organization_cost_center", "cost_binding_stale", "tenant_stale_cost", 1, map[string]interface{}{"externalId": "cost_binding_stale", "organizationExternalId": "org_cost_old", "costCenterExternalId": "cc_old"}),
			gatewayIntegrationEvent("evt_stale_cost_binding_v2", "organization_cost_center.updated", "organization_cost_center", "cost_binding_stale", "tenant_stale_cost", 2, map[string]interface{}{"externalId": "cost_binding_stale", "organizationExternalId": "org_cost_current", "costCenterExternalId": "cc_current"}),
		} {
			applyGatewayIntegrationEventForTest(t, app, event)
		}
		if err := store.db.Unscoped().Where("external_organization_id = ?", "org_cost_old").Delete(&GatewayOrganization{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Unscoped().Where("external_cost_center_id = ?", "cc_old").Delete(&GatewayCostCenter{}).Error; err != nil {
			t.Fatal(err)
		}

		response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", gatewayIntegrationEvent("evt_stale_cost_binding_replay", "organization_cost_center.created", "organization_cost_center", "cost_binding_stale", "tenant_stale_cost", 1, map[string]interface{}{"externalId": "cost_binding_stale", "organizationExternalId": "org_cost_old", "costCenterExternalId": "cc_old"}), "integration_token")
		if response.Code != http.StatusOK || !jsonBodyHasField(response.Body, "outcome", "ignored_stale") {
			t.Fatalf("stale organization cost center replay = %d %s", response.Code, response.Body)
		}
		var binding GatewayOrganizationCostCenter
		if err := store.db.First(&binding, "external_binding_id = ?", "cost_binding_stale").Error; err != nil {
			t.Fatal(err)
		}
		var costCenter GatewayCostCenter
		if err := store.db.First(&costCenter, "id = ?", binding.CostCenterID).Error; err != nil {
			t.Fatal(err)
		}
		if costCenter.ExternalCostCenterID != "cc_current" || binding.Version != 2 {
			t.Fatalf("stale replay changed current cost center binding: binding=%+v costCenter=%+v", binding, costCenter)
		}
	})

	t.Run("workload", func(t *testing.T) {
		store := NewMemoryStore()
		app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
		for _, event := range []map[string]interface{}{
			gatewayIntegrationEvent("evt_stale_workload_tenant", "tenant.created", "tenant", "tenant_stale_workload", "tenant_stale_workload", 1, map[string]interface{}{"externalId": "tenant_stale_workload", "name": "Stale workload tenant"}),
			gatewayIntegrationEvent("evt_stale_workload_project_old", "project.created", "project", "project_old", "tenant_stale_workload", 1, map[string]interface{}{"externalId": "project_old", "name": "Old project"}),
			gatewayIntegrationEvent("evt_stale_workload_project_current", "project.created", "project", "project_current", "tenant_stale_workload", 1, map[string]interface{}{"externalId": "project_current", "name": "Current project"}),
			gatewayIntegrationEvent("evt_stale_workload_v1", "workload.created", "workload", "workload_stale", "tenant_stale_workload", 1, map[string]interface{}{"externalId": "workload_stale", "projectExternalId": "project_old", "name": "Stale workload"}),
			gatewayIntegrationEvent("evt_stale_workload_v2", "workload.updated", "workload", "workload_stale", "tenant_stale_workload", 2, map[string]interface{}{"externalId": "workload_stale", "projectExternalId": "project_current", "name": "Current workload"}),
		} {
			applyGatewayIntegrationEventForTest(t, app, event)
		}
		if err := store.db.Unscoped().Where("external_project_id = ?", "project_old").Delete(&GatewayProject{}).Error; err != nil {
			t.Fatal(err)
		}

		response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", gatewayIntegrationEvent("evt_stale_workload_replay", "workload.created", "workload", "workload_stale", "tenant_stale_workload", 1, map[string]interface{}{"externalId": "workload_stale", "projectExternalId": "project_old", "name": "Stale workload"}), "integration_token")
		if response.Code != http.StatusOK || !jsonBodyHasField(response.Body, "outcome", "ignored_stale") {
			t.Fatalf("stale workload replay = %d %s", response.Code, response.Body)
		}
		var workload GatewayWorkload
		if err := store.db.First(&workload, "external_workload_id = ?", "workload_stale").Error; err != nil {
			t.Fatal(err)
		}
		var project GatewayProject
		if err := store.db.First(&project, "id = ?", workload.ProjectID).Error; err != nil {
			t.Fatal(err)
		}
		if project.ExternalProjectID != "project_current" || workload.Version != 2 {
			t.Fatalf("stale replay changed current workload: workload=%+v project=%+v", workload, project)
		}
	})
}

func TestGatewayOrganizationBindingHandlesSoftDeletedPrincipals(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_soft_deleted_binding_tenant", "tenant.created", "tenant", "tenant_soft_deleted_binding", "tenant_soft_deleted_binding", 1, map[string]interface{}{"externalId": "tenant_soft_deleted_binding", "name": "Soft deleted binding tenant"}),
		gatewayIntegrationEvent("evt_soft_deleted_binding_principal_old", "tenant_member.added", "tenant_member", "member_soft_deleted_old", "tenant_soft_deleted_binding", 1, map[string]interface{}{"externalId": "member_soft_deleted_old", "principalExternalId": "principal_soft_deleted_old", "name": "Old principal", "email": "old@example.test"}),
		gatewayIntegrationEvent("evt_soft_deleted_binding_org", "organization.created", "organization", "org_soft_deleted_binding", "tenant_soft_deleted_binding", 1, map[string]interface{}{"externalId": "org_soft_deleted_binding", "name": "Organization"}),
		gatewayIntegrationEvent("evt_soft_deleted_binding_v1", "organization_member.added", "organization_member", "binding_soft_deleted", "tenant_soft_deleted_binding", 1, map[string]interface{}{"externalId": "binding_soft_deleted", "principalExternalId": "principal_soft_deleted_old", "organizationExternalId": "org_soft_deleted_binding"}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_soft_deleted_binding_principal_removed", "tenant_member.deleted", "tenant_member", "member_soft_deleted_old", "tenant_soft_deleted_binding", 2, map[string]interface{}{
		"externalId": "member_soft_deleted_old", "principalExternalId": "principal_soft_deleted_old", "status": integrationStatusDeleted,
	}))

	stale := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", gatewayIntegrationEvent(
		"evt_soft_deleted_binding_replay", "organization_member.added", "organization_member", "binding_soft_deleted", "tenant_soft_deleted_binding", 1,
		map[string]interface{}{"externalId": "binding_soft_deleted", "principalExternalId": "principal_soft_deleted_old", "organizationExternalId": "org_soft_deleted_binding"},
	), "integration_token")
	if stale.Code != http.StatusOK || !jsonBodyHasField(stale.Body, "outcome", "ignored_stale") {
		t.Fatalf("soft-deleted principal blocked stale binding replay: %d %s", stale.Code, stale.Body)
	}

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_soft_deleted_binding_principal_new", "tenant_member.added", "tenant_member", "member_soft_deleted_new", "tenant_soft_deleted_binding", 1, map[string]interface{}{
		"externalId": "member_soft_deleted_new", "principalExternalId": "principal_soft_deleted_new", "name": "New principal", "email": "new@example.test",
	}))
	rebound := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", gatewayIntegrationEvent(
		"evt_soft_deleted_binding_rebound", "organization_member.updated", "organization_member", "binding_soft_deleted", "tenant_soft_deleted_binding", 2,
		map[string]interface{}{"externalId": "binding_soft_deleted", "principalExternalId": "principal_soft_deleted_new", "organizationExternalId": "org_soft_deleted_binding"},
	), "integration_token")
	if rebound.Code != http.StatusOK || !jsonBodyHasField(rebound.Body, "outcome", "applied") {
		t.Fatalf("soft-deleted principal blocked binding rebind: %d %s", rebound.Code, rebound.Body)
	}
}
