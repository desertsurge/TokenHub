package server

import (
	"testing"
	"time"
)

func TestGatewayManagedUserLockScopeIncludesDeletedProjections(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	deletedAt := now.Add(-time.Minute)
	tenant := GatewayTenant{ID: "tenant_deleted_lock", ExternalTenantID: "tenant_deleted_lock", Status: StatusActive, Version: 1, SyncedAt: now, DeletedAt: &deletedAt}
	principal := GatewayPrincipal{ID: "principal_deleted_lock", TenantID: tenant.ID, ExternalPrincipalID: "principal_deleted_lock", Status: StatusActive, Version: 1, SyncedAt: now, DeletedAt: &deletedAt}
	if err := store.db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}

	ids, err := gatewayManagedUserIDsForIntegrationEvent(store.db, GatewayIntegrationEvent{
		AggregateType: "tenant",
		TenantID:      tenant.ExternalTenantID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != principal.ExternalPrincipalID {
		t.Fatalf("deleted tenant/principal were omitted from lock scope: %v", ids)
	}

	organization := GatewayOrganization{ID: "organization_deleted_lock", TenantID: tenant.ID, ExternalOrganizationID: "organization_deleted_lock", Status: StatusActive, Version: 1, SyncedAt: now, DeletedAt: &deletedAt}
	binding := GatewayPrincipalOrganizationBinding{ID: "binding_deleted_lock", TenantID: tenant.ID, ExternalMembershipID: "binding_deleted_lock", PrincipalID: principal.ID, OrganizationID: organization.ID, Status: StatusActive, Version: 1, SyncedAt: now, DeletedAt: &deletedAt}
	if err := store.db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	ids, err = gatewayManagedUserIDsForIntegrationEvent(store.db, GatewayIntegrationEvent{
		AggregateType: "organization_member",
		AggregateID:   binding.ExternalMembershipID,
		TenantID:      tenant.ExternalTenantID,
		Payload:       map[string]interface{}{"principalExternalId": "new_principal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen[principal.ExternalPrincipalID] || !seen["new_principal"] {
		t.Fatalf("deleted binding/principal were omitted from lock scope: %v", ids)
	}
}
