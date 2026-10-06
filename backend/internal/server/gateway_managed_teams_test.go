package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestGatewayOrganizationPrimaryRequiresBoolean(t *testing.T) {
	event := GatewayIntegrationEvent{
		AggregateType: "organization_member",
		Payload: map[string]interface{}{
			"principalExternalId":    "principal_1",
			"organizationExternalId": "organization_1",
			"isPrimary":              "true",
		},
	}
	if err := validateGatewayIntegrationPayload(event); err == nil {
		t.Fatal("non-boolean organization primary flag was accepted")
	}
	delete(event.Payload, "isPrimary")
	if err := validateGatewayIntegrationPayload(event); err != nil {
		t.Fatalf("legacy organization member event was rejected: %v", err)
	}
}

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
			"externalId": "binding_a", "principalExternalId": "shared_principal", "organizationExternalId": "org_a", "isPrimary": true,
		}),
		gatewayIntegrationEvent("evt_team_b_binding", "organization_member.added", "organization_member", "binding_b", "tenant_b", 1, map[string]interface{}{
			"externalId": "binding_b", "principalExternalId": "shared_principal", "organizationExternalId": "org_b", "isPrimary": true,
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
	if user.TeamID != "" {
		t.Fatalf("conflicting cross-tenant primary bindings must not choose an arbitrary team: %s", user.TeamID)
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

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_b_org_reactivated", "organization.updated", "organization", "org_b", "tenant_b", 3, map[string]interface{}{
		"externalId": "org_b", "name": "Organization B", "status": StatusActive,
	}))
	loadUser()
	if !userHasTeam(user, teamB.ID) || user.TeamID != teamB.ID || !userHasTeam(user, "team_local") {
		t.Fatalf("reactivated organization did not restore team membership: primary=%s teams=%v", user.TeamID, user.TeamIDs)
	}

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_b_org_deleted", "organization.deleted", "organization", "org_b", "tenant_b", 4, map[string]interface{}{
		"externalId": "org_b", "name": "Organization B", "status": integrationStatusDeleted,
	}))
	loadUser()
	if userHasTeam(user, teamB.ID) || user.TeamID != "" || !userHasTeam(user, "team_local") {
		t.Fatalf("deleted organization retained team membership: primary=%s teams=%v", user.TeamID, user.TeamIDs)
	}
}

func TestGatewayManagedUserPrimaryTeamFollowsBinding(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_primary_tenant", "tenant.created", "tenant", "tenant_primary", "tenant_primary", 1, map[string]interface{}{"externalId": "tenant_primary", "name": "Tenant"}),
		gatewayIntegrationEvent("evt_primary_org_a", "organization.created", "organization", "org_primary_a", "tenant_primary", 1, map[string]interface{}{"externalId": "org_primary_a", "name": "Organization A"}),
		gatewayIntegrationEvent("evt_primary_org_b", "organization.created", "organization", "org_primary_b", "tenant_primary", 1, map[string]interface{}{"externalId": "org_primary_b", "name": "Organization B"}),
		gatewayIntegrationEvent("evt_primary_principal", "tenant_member.added", "tenant_member", "member_primary", "tenant_primary", 1, map[string]interface{}{
			"externalId": "member_primary", "principalExternalId": "principal_primary", "name": "Primary User", "email": "primary-team@example.com",
		}),
		gatewayIntegrationEvent("evt_primary_binding_a", "organization_member.added", "organization_member", "binding_primary_a", "tenant_primary", 1, map[string]interface{}{
			"externalId": "binding_primary_a", "principalExternalId": "principal_primary", "organizationExternalId": "org_primary_a", "isPrimary": true,
		}),
		gatewayIntegrationEvent("evt_primary_binding_b", "organization_member.added", "organization_member", "binding_primary_b", "tenant_primary", 1, map[string]interface{}{
			"externalId": "binding_primary_b", "principalExternalId": "principal_primary", "organizationExternalId": "org_primary_b", "isPrimary": false,
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}
	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "principal_primary").Error; err != nil {
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
	if user.TeamID != teamA.ID || !userHasTeam(user, teamB.ID) {
		t.Fatalf("explicit primary team was not projected: primary=%s teams=%v", user.TeamID, user.TeamIDs)
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_primary_binding_a_clear", "organization_member.updated", "organization_member", "binding_primary_a", "tenant_primary", 2, map[string]interface{}{
		"externalId": "binding_primary_a", "principalExternalId": "principal_primary", "organizationExternalId": "org_primary_a", "isPrimary": false,
	}))
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_primary_binding_b_set", "organization_member.updated", "organization_member", "binding_primary_b", "tenant_primary", 2, map[string]interface{}{
		"externalId": "binding_primary_b", "principalExternalId": "principal_primary", "organizationExternalId": "org_primary_b", "isPrimary": true,
	}))
	loadUser()
	if user.TeamID != teamB.ID {
		t.Fatalf("primary team did not follow the updated binding: %s", user.TeamID)
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_primary_binding_b_without_primary", "organization_member.updated", "organization_member", "binding_primary_b", "tenant_primary", 3, map[string]interface{}{
		"externalId": "binding_primary_b", "principalExternalId": "principal_primary", "organizationExternalId": "org_primary_b",
	}))
	loadUser()
	if user.TeamID != teamB.ID {
		t.Fatalf("omitted primary flag cleared the existing primary team: %s", user.TeamID)
	}
	user.TeamID = "team_local"
	user.TeamIDs = append(user.TeamIDs, "team_local")
	if err := store.db.Save(&user).Error; err != nil {
		t.Fatal(err)
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_primary_binding_b_repeat", "organization_member.updated", "organization_member", "binding_primary_b", "tenant_primary", 4, map[string]interface{}{
		"externalId": "binding_primary_b", "principalExternalId": "principal_primary", "organizationExternalId": "org_primary_b", "isPrimary": true,
	}))
	loadUser()
	if user.TeamID != "team_local" || !userHasTeam(user, teamB.ID) {
		t.Fatalf("managed binding replaced a local primary team: primary=%s teams=%v", user.TeamID, user.TeamIDs)
	}
}

func TestGatewayManagedUserTeamsPreserveLocalTeamWithManagedIDShape(t *testing.T) {
	store := NewMemoryStore()
	config := Config{AdminToken: "admin_token", IntegrationToken: "integration_token", SecretKey: "test_secret"}
	app := NewWithConfig(store, config).Handler()
	localTeamID := "gwt_0123456789abcdef01234567"
	created := doJSON(t, app, http.MethodPost, "/api/admin/resources/teams", map[string]any{
		"id": localTeamID, "name": "Local team",
	}, config.AdminToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create local team through admin API: status=%d body=%s", created.Code, created.Body)
	}

	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_local_shape_tenant", "tenant.created", "tenant", "tenant_local_shape", "tenant_local_shape", 1, map[string]interface{}{"externalId": "tenant_local_shape", "name": "Tenant"}),
		gatewayIntegrationEvent("evt_local_shape_principal", "tenant_member.added", "tenant_member", "member_local_shape", "tenant_local_shape", 1, map[string]interface{}{
			"externalId": "member_local_shape", "principalExternalId": "principal_local_shape", "name": "Local Team User", "email": "local-team-shape@example.com",
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}
	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "principal_local_shape").Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	user.TeamID, user.TeamIDs = localTeamID, []string{localTeamID}
	if err := store.db.Save(&user).Error; err != nil {
		t.Fatal(err)
	}

	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_local_shape_org", "organization.created", "organization", "org_local_shape", "tenant_local_shape", 1, map[string]interface{}{"externalId": "org_local_shape", "name": "Organization"}),
		gatewayIntegrationEvent("evt_local_shape_binding", "organization_member.added", "organization_member", "binding_local_shape", "tenant_local_shape", 1, map[string]interface{}{
			"externalId": "binding_local_shape", "principalExternalId": "principal_local_shape", "organizationExternalId": "org_local_shape", "isPrimary": true,
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.TeamID != localTeamID || !userHasTeam(user, localTeamID) {
		t.Fatalf("managed team refresh removed a local team with a managed ID shape: primary=%s teams=%v", user.TeamID, user.TeamIDs)
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
	if err := store.db.First(&managed, "external_principal_id = ?", "principal_late").Error; err != nil {
		t.Fatal("managed user was not created without an email", err)
	}
	var synthetic AdminUser
	if err := store.db.First(&synthetic, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(synthetic.Email, "@users.tokenhub.invalid") {
		t.Fatalf("expected a non-login synthetic email for a principal without email, got %q", synthetic.Email)
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
