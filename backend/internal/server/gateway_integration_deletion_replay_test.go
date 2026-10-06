package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGatewayDeletionReplaysRevokeModelAccessKeys(t *testing.T) {
	t.Run("tenant", func(t *testing.T) {
		store := NewMemoryStore()
		app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
		seedGatewayModelAccessKeyScope(t, app)
		created := createGatewayModelAccessKeyForTest(t, app, "request_tenant_delete_replay", "user", "user_01")
		applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_tenant_delete_initial", "tenant.deleted", "tenant", "tenant_01", "tenant_01", 2, map[string]interface{}{
			"externalId": "tenant_01", "status": integrationStatusDeleted,
		}))
		driftGatewayModelAccessKeyActive(t, store, created.Data.ID)
		applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_tenant_delete_replay", "tenant.deleted", "tenant", "tenant_01", "tenant_01", 2, map[string]interface{}{
			"externalId": "tenant_01", "status": integrationStatusDeleted,
		}))
		assertGatewayModelAccessKeyRevoked(t, store, created)
	})

	t.Run("organization", func(t *testing.T) {
		store := NewMemoryStore()
		app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
		for _, event := range []map[string]interface{}{
			gatewayIntegrationEvent("evt_org_delete_tenant", "tenant.created", "tenant", "tenant_org_delete", "tenant_org_delete", 1, map[string]interface{}{
				"externalId": "tenant_org_delete", "name": "Organization delete tenant",
			}),
			gatewayIntegrationEvent("evt_org_delete_principal", "tenant_member.added", "tenant_member", "membership_org_delete", "tenant_org_delete", 1, map[string]interface{}{
				"externalId": "membership_org_delete", "principalExternalId": "principal_org_delete", "name": "Organization user", "email": "organization-user@example.com",
			}),
			gatewayIntegrationEvent("evt_org_delete_created", "organization.created", "organization", "org_delete", "tenant_org_delete", 1, map[string]interface{}{
				"externalId": "org_delete", "name": "Deleted organization",
			}),
			gatewayIntegrationEvent("evt_org_delete_membership", "organization_member.added", "organization_member", "org_membership_delete", "tenant_org_delete", 1, map[string]interface{}{
				"externalId": "org_membership_delete", "principalExternalId": "principal_org_delete", "organizationExternalId": "org_delete",
			}),
			gatewayIntegrationEvent("evt_org_delete_project", "project.created", "project", "project_org_delete", "tenant_org_delete", 1, map[string]interface{}{
				"externalId": "project_org_delete", "name": "Organization project", "ownerExternalId": "principal_org_delete", "organizationExternalId": "org_delete",
			}),
		} {
			applyGatewayIntegrationEventForTest(t, app, event)
		}
		created := createGatewayModelAccessKeyForScopeTest(t, app, "request_org_delete_replay", "tenant_org_delete", "project_org_delete", "user", "principal_org_delete")
		applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_org_delete_initial", "organization.deleted", "organization", "org_delete", "tenant_org_delete", 2, map[string]interface{}{
			"externalId": "org_delete", "status": integrationStatusDeleted,
		}))
		driftGatewayModelAccessKeyActive(t, store, created.Data.ID)
		applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_org_delete_replay", "organization.deleted", "organization", "org_delete", "tenant_org_delete", 2, map[string]interface{}{
			"externalId": "org_delete", "status": integrationStatusDeleted,
		}))
		assertGatewayModelAccessKeyRevoked(t, store, created)
	})

	t.Run("principal", func(t *testing.T) {
		store := NewMemoryStore()
		app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
		seedGatewayModelAccessKeyScope(t, app)
		created := createGatewayModelAccessKeyForTest(t, app, "request_principal_delete_replay", "user", "user_01")
		applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_principal_delete_initial", "tenant_member.removed", "tenant_member", "membership_01", "tenant_01", 2, map[string]interface{}{
			"externalId": "membership_01", "principalExternalId": "user_01", "status": "removed",
		}))
		driftGatewayModelAccessKeyActive(t, store, created.Data.ID)
		applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_principal_delete_replay", "tenant_member.removed", "tenant_member", "membership_01", "tenant_01", 2, map[string]interface{}{
			"externalId": "membership_01", "principalExternalId": "user_01", "status": "removed",
		}))
		assertGatewayModelAccessKeyRevoked(t, store, created)
	})

	t.Run("project", func(t *testing.T) {
		store := NewMemoryStore()
		app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
		seedGatewayModelAccessKeyScope(t, app)
		created := createGatewayModelAccessKeyForTest(t, app, "request_project_delete_replay", "user", "user_01")
		applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_project_delete_initial", "project.deleted", "project", "project_01", "tenant_01", 2, map[string]interface{}{
			"externalId": "project_01", "status": integrationStatusDeleted,
		}))
		driftGatewayModelAccessKeyActive(t, store, created.Data.ID)
		applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_project_delete_replay", "project.deleted", "project", "project_01", "tenant_01", 2, map[string]interface{}{
			"externalId": "project_01", "status": integrationStatusDeleted,
		}))
		assertGatewayModelAccessKeyRevoked(t, store, created)
	})

	t.Run("workload", func(t *testing.T) {
		store := NewMemoryStore()
		app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
		seedGatewayModelAccessKeyScope(t, app)
		workload := map[string]interface{}{
			"externalId": "application_delete_replay", "projectExternalId": "project_01", "ownerExternalId": "user_01",
			"name": "Replay application", "workloadType": "application", "status": StatusActive,
		}
		applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_workload_delete_created", "workload.created", "workload", "application_delete_replay", "tenant_01", 1, workload))
		created := createGatewayModelAccessKeyForTest(t, app, "request_workload_delete_replay", "application", "application_delete_replay")
		workload["status"] = integrationStatusDeleted
		applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_workload_delete_initial", "workload.deleted", "workload", "application_delete_replay", "tenant_01", 2, workload))
		driftGatewayModelAccessKeyActive(t, store, created.Data.ID)
		applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_workload_delete_replay", "workload.deleted", "workload", "application_delete_replay", "tenant_01", 2, workload))
		assertGatewayModelAccessKeyRevoked(t, store, created)
	})
}

func createGatewayModelAccessKeyForScopeTest(t *testing.T, app http.Handler, requestID, tenantID, projectID, principalType, principalID string) gatewayModelAccessKeyCreateResponse {
	t.Helper()
	response := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys", map[string]interface{}{
		"request_id": requestID, "tenant_id": tenantID, "project_id": projectID,
		"principal_type": principalType, "principal_id": principalID, "name": requestID, "requested_by": principalID,
	}, "integration_token")
	if response.Code != http.StatusCreated {
		t.Fatalf("expected model access key creation, got %d: %s", response.Code, response.Body)
	}
	var result gatewayModelAccessKeyCreateResponse
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func driftGatewayModelAccessKeyActive(t *testing.T, store *GormStore, keyID string) {
	t.Helper()
	if err := store.db.Model(&APIKey{}).Where("id = ?", keyID).Update("status", StatusActive).Error; err != nil {
		t.Fatal(err)
	}
}
