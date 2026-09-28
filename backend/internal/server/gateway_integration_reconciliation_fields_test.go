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
