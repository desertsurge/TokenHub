package server

import "testing"

func TestGatewayManagedUserTeamsFollowMembershipAcrossTenants(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_team_a_tenant", "tenant.created", "tenant", "tenant_a", "tenant_a", 1, map[string]interface{}{"externalId": "tenant_a", "name": "Tenant A"}),
		gatewayIntegrationEvent("evt_team_b_tenant", "tenant.created", "tenant", "tenant_b", "tenant_b", 1, map[string]interface{}{"externalId": "tenant_b", "name": "Tenant B"}),
		gatewayIntegrationEvent("evt_team_a_org", "organization.created", "organization", "org_a", "tenant_a", 1, map[string]interface{}{"externalId": "org_a", "name": "Organization A"}),
		gatewayIntegrationEvent("evt_team_b_org", "organization.created", "organization", "org_b", "tenant_b", 1, map[string]interface{}{"externalId": "org_b", "name": "Organization B"}),
		gatewayIntegrationEvent("evt_team_a_principal", "tenant_member.added", "tenant_member", "member_a", "tenant_a", 1, map[string]interface{}{
			"externalId": "member_a", "principalExternalId": "shared_principal", "name": "Shared User", "email": "shared-teams@example.com",
		}),
		gatewayIntegrationEvent("evt_team_b_principal", "tenant_member.added", "tenant_member", "member_b", "tenant_b", 1, map[string]interface{}{
			"externalId": "member_b", "principalExternalId": "shared_principal", "name": "Shared User", "email": "shared-teams@example.com",
		}),
		gatewayIntegrationEvent("evt_team_a_binding", "organization_member.added", "organization_member", "binding_a", "tenant_a", 1, map[string]interface{}{
			"externalId": "binding_a", "principalExternalId": "shared_principal", "organizationExternalId": "org_a",
		}),
		gatewayIntegrationEvent("evt_team_b_binding", "organization_member.added", "organization_member", "binding_b", "tenant_b", 1, map[string]interface{}{
			"externalId": "binding_b", "principalExternalId": "shared_principal", "organizationExternalId": "org_b",
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}
	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "shared_principal").Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	loadUser := func() {
		if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
			t.Fatal(err)
		}
	}
	var teamA, teamB AdminResource
	if err := store.db.First(&teamA, "kind = ? AND name = ?", "teams", "Organization A").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.First(&teamB, "kind = ? AND name = ?", "teams", "Organization B").Error; err != nil {
		t.Fatal(err)
	}
	loadUser()
	if !userHasTeam(user, teamA.ID) || !userHasTeam(user, teamB.ID) {
		t.Fatalf("cross-tenant team memberships were not projected: %+v", user.TeamIDs)
	}

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_a_binding_removed", "organization_member.removed", "organization_member", "binding_a", "tenant_a", 2, map[string]interface{}{
		"externalId": "binding_a", "principalExternalId": "shared_principal", "organizationExternalId": "org_a", "status": StatusDisabled,
	}))
	loadUser()
	if userHasTeam(user, teamA.ID) || !userHasTeam(user, teamB.ID) || user.TeamID != teamB.ID {
		t.Fatalf("membership removal changed the wrong tenant team: primary=%s teams=%v", user.TeamID, user.TeamIDs)
	}
	user.TeamIDs = append(user.TeamIDs, "team_local")
	if err := store.db.Save(&user).Error; err != nil {
		t.Fatal(err)
	}

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_b_org_disabled", "organization.disabled", "organization", "org_b", "tenant_b", 2, map[string]interface{}{
		"externalId": "org_b", "name": "Organization B", "status": StatusDisabled,
	}))
	loadUser()
	if userHasTeam(user, teamB.ID) || user.TeamID != "" || !userHasTeam(user, "team_local") {
		t.Fatalf("disabled organization retained team membership: primary=%s teams=%v", user.TeamID, user.TeamIDs)
	}
}

func TestGatewayManagedUserCreatedAfterOrganizationBindingReceivesTeam(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_late_team_tenant", "tenant.created", "tenant", "tenant_late", "tenant_late", 1, map[string]interface{}{"externalId": "tenant_late", "name": "Tenant"}),
		gatewayIntegrationEvent("evt_late_team_org", "organization.created", "organization", "org_late", "tenant_late", 1, map[string]interface{}{"externalId": "org_late", "name": "Organization"}),
		gatewayIntegrationEvent("evt_late_team_principal", "tenant_member.added", "tenant_member", "member_late", "tenant_late", 1, map[string]interface{}{
			"externalId": "member_late", "principalExternalId": "principal_late", "name": "Late User",
		}),
		gatewayIntegrationEvent("evt_late_team_binding", "organization_member.added", "organization_member", "binding_late", "tenant_late", 1, map[string]interface{}{
			"externalId": "binding_late", "principalExternalId": "principal_late", "organizationExternalId": "org_late",
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}
	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "principal_late").Error; err == nil {
		t.Fatal("managed user was created without an email")
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_late_team_principal_email", "tenant_member.updated", "tenant_member", "member_late", "tenant_late", 2, map[string]interface{}{
		"externalId": "member_late", "principalExternalId": "principal_late", "name": "Late User", "email": "late-team@example.com",
	}))
	if err := store.db.First(&managed, "external_principal_id = ?", "principal_late").Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if len(user.TeamIDs) != 1 || user.TeamID != user.TeamIDs[0] {
		t.Fatalf("late-created user did not receive the organization team: %+v", user)
	}
}
