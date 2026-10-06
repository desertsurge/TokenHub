package server

import (
	"bytes"
	"testing"
)

func TestGatewayProjectionDigestIncludesActiveIdentityAndCostFields(t *testing.T) {
	member := gatewayProjectionDigestItem{
		ExternalID: "membership-1", PrincipalID: "profile-1", Name: "User", Email: "user@example.com",
		Status: StatusActive, Version: 1,
	}
	changedMember := member
	changedMember.Email = "other@example.com"
	if bytes.Equal(canonicalGatewayProjectionDigest([]gatewayProjectionDigestItem{member}), canonicalGatewayProjectionDigest([]gatewayProjectionDigestItem{changedMember})) {
		t.Fatal("managed-user email drift did not change the projection digest")
	}

	costCenter := gatewayProjectionDigestItem{ExternalID: "cost-center-1", Name: "Engineering", Code: "CC-ENG", Status: StatusActive, Version: 1}
	changedCostCenter := costCenter
	changedCostCenter.Code = "CC-OTHER"
	if bytes.Equal(canonicalGatewayProjectionDigest([]gatewayProjectionDigestItem{costCenter}), canonicalGatewayProjectionDigest([]gatewayProjectionDigestItem{changedCostCenter})) {
		t.Fatal("cost-center code drift did not change the projection digest")
	}
}

func TestGatewayProjectionDigestIgnoresDeletedDisplayFields(t *testing.T) {
	deletedWithDisplayFields := gatewayProjectionDigestItem{
		ExternalID: "cost-center-1", Name: "Old center", Code: "CC-OLD",
		Status: integrationStatusDeleted, Version: 2, Deleted: true,
	}
	deletedWithoutDisplayFields := gatewayProjectionDigestItem{
		ExternalID: "cost-center-1", Status: integrationStatusDeleted, Version: 2, Deleted: true,
	}
	if !bytes.Equal(canonicalGatewayProjectionDigest([]gatewayProjectionDigestItem{deletedWithDisplayFields}), canonicalGatewayProjectionDigest([]gatewayProjectionDigestItem{deletedWithoutDisplayFields})) {
		t.Fatal("deleted display fields caused a false projection drift")
	}
}

func TestGatewayReconciliationDetailsExposeManagedEmailAndCostCenterCode(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_reconcile_tenant", "tenant.created", "tenant", "tenant_reconcile", "tenant_reconcile", 1, map[string]interface{}{
			"externalId": "tenant_reconcile", "name": "Reconciliation tenant",
		}),
		gatewayIntegrationEvent("evt_reconcile_member", "tenant_member.added", "tenant_member", "membership_reconcile", "tenant_reconcile", 1, map[string]interface{}{
			"externalId": "membership_reconcile", "principalExternalId": "profile_reconcile", "name": "User", "email": "user@example.com",
		}),
		gatewayIntegrationEvent("evt_reconcile_center", "cost_center.created", "cost_center", "cost_center_reconcile", "tenant_reconcile", 1, map[string]interface{}{
			"externalId": "cost_center_reconcile", "code": "CC-RECONCILE", "name": "Reconciliation center",
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}

	summary, err := store.GetGatewayIntegrationReconciliationWithDetails("tenant_reconcile")
	if err != nil {
		t.Fatal(err)
	}
	if members := summary.ProjectionDetails["tenant_member"]; len(members) != 1 || members[0].Email != "user@example.com" {
		t.Fatalf("expected managed-user email in reconciliation details, got %+v", members)
	}
	if centers := summary.ProjectionDetails["cost_center"]; len(centers) != 1 || centers[0].Code != "CC-RECONCILE" {
		t.Fatalf("expected cost-center code in reconciliation details, got %+v", centers)
	}
}

func TestGatewayReconciliationRetainsDeletedProjectionRows(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_reconcile_deleted_tenant", "tenant.created", "tenant", "tenant_reconcile_deleted", "tenant_reconcile_deleted", 1, map[string]interface{}{
		"externalId": "tenant_reconcile_deleted", "name": "Deleted projection tenant",
	}))
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_reconcile_deleted_center", "cost_center.created", "cost_center", "cost_center_deleted", "tenant_reconcile_deleted", 1, map[string]interface{}{
		"externalId": "cost_center_deleted", "code": "CC-DELETED", "name": "Deleted center",
	}))
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_reconcile_deleted_center_event", "cost_center.deleted", "cost_center", "cost_center_deleted", "tenant_reconcile_deleted", 2, map[string]interface{}{
		"externalId": "cost_center_deleted", "status": integrationStatusDeleted,
	}))

	summary, err := store.GetGatewayIntegrationReconciliationWithDetails("tenant_reconcile_deleted")
	if err != nil {
		t.Fatal(err)
	}
	centers := summary.ProjectionDetails["cost_center"]
	if len(centers) != 1 || !centers[0].Deleted || centers[0].Version != 2 || centers[0].Status != integrationStatusDeleted {
		t.Fatalf("deleted cost-center projection was omitted from reconciliation: %+v", centers)
	}
}

func TestGatewayReconciliationKeepsTenantScopedPrincipalEmail(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_email_tenant_a", "tenant.created", "tenant", "tenant_email_a", "tenant_email_a", 1, map[string]interface{}{
			"externalId": "tenant_email_a", "name": "Tenant A",
		}),
		gatewayIntegrationEvent("evt_email_member_a", "tenant_member.added", "tenant_member", "membership_email_a", "tenant_email_a", 1, map[string]interface{}{
			"externalId": "membership_email_a", "principalExternalId": "profile_shared_email", "name": "User A", "email": "tenant-a@example.com",
		}),
		gatewayIntegrationEvent("evt_email_tenant_b", "tenant.created", "tenant", "tenant_email_b", "tenant_email_b", 1, map[string]interface{}{
			"externalId": "tenant_email_b", "name": "Tenant B",
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}
	memberB := gatewayIntegrationEvent("evt_email_member_b", "tenant_member.added", "tenant_member", "membership_email_b", "tenant_email_b", 1, map[string]interface{}{
		"externalId": "membership_email_b", "principalExternalId": "profile_shared_email", "name": "User B", "email": "tenant-b@example.com",
	})
	memberB["occurredAt"] = "2026-07-25T08:00:00.000Z"
	applyGatewayIntegrationEventForTest(t, app, memberB)

	summary, err := store.GetGatewayIntegrationReconciliationWithDetails("tenant_email_a")
	if err != nil {
		t.Fatal(err)
	}
	members := summary.ProjectionDetails["tenant_member"]
	if len(members) != 1 || members[0].Email != "tenant-a@example.com" {
		t.Fatalf("tenant A reconciliation used another tenant's managed email: %+v", members)
	}
}
