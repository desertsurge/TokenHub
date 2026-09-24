package server

import "testing"

func TestGatewayOrganizationCreatesTeamWithoutProject(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_tenant", "tenant.created", "tenant", "tenant_team", "tenant_team", 1, map[string]interface{}{
		"externalId": "tenant_team", "name": "Team tenant",
	}))
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_org", "organization.created", "organization", "org_team", "tenant_team", 1, map[string]interface{}{
		"externalId": "org_team", "name": "Engineering",
	}))

	var team AdminResource
	if err := store.db.First(&team, "kind = ? AND name = ?", "teams", "Engineering").Error; err != nil {
		t.Fatal(err)
	}
	if team.Status != StatusActive || team.Fields["external_organization_id"] != "org_team" {
		t.Fatalf("organization team was not projected: %+v", team)
	}
	if err := store.db.Delete(&team).Error; err != nil {
		t.Fatal(err)
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_org_replay", "organization.updated", "organization", "org_team", "tenant_team", 1, map[string]interface{}{
		"externalId": "org_team", "name": "Engineering",
	}))
	if err := store.db.First(&team, "id = ?", team.ID).Error; err != nil {
		t.Fatalf("stale organization replay did not restore the team: %v", err)
	}

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_org_disabled", "organization.disabled", "organization", "org_team", "tenant_team", 2, map[string]interface{}{
		"externalId": "org_team", "name": "Engineering", "status": StatusDisabled,
	}))
	if err := store.db.First(&team, "id = ?", team.ID).Error; err != nil {
		t.Fatal(err)
	}
	if team.Status != StatusDisabled {
		t.Fatalf("disabled organization retained an active team: %+v", team)
	}
}

func TestGatewayProjectTeamHasViewerRoleAndClearsWhenOrganizationRemoved(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_project_team_tenant", "tenant.created", "tenant", "tenant_team", "tenant_team", 1, map[string]interface{}{"externalId": "tenant_team", "name": "Team tenant"}),
		gatewayIntegrationEvent("evt_project_team_org", "organization.created", "organization", "org_team", "tenant_team", 1, map[string]interface{}{"externalId": "org_team", "name": "Engineering"}),
		gatewayIntegrationEvent("evt_project_team_owner", "tenant_member.added", "tenant_member", "member_team", "tenant_team", 1, map[string]interface{}{
			"externalId": "member_team", "principalExternalId": "principal_team", "name": "Owner", "email": "team-owner@example.com",
		}),
		gatewayIntegrationEvent("evt_project_team_create", "project.created", "project", "project_team", "tenant_team", 1, map[string]interface{}{
			"externalId": "project_team", "name": "Project", "organizationExternalId": "org_team", "ownerExternalId": "principal_team",
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}
	var projection GatewayProject
	if err := store.db.First(&projection, "external_project_id = ?", "project_team").Error; err != nil {
		t.Fatal(err)
	}
	project, found := store.GetProject(projection.ID)
	if !found || project.TeamID == "" || project.OwnerUserID == "" {
		t.Fatalf("project was not linked to its organization and owner: %+v", project)
	}
	var link ProjectTeam
	if err := store.db.First(&link, "project_id = ?", project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if link.TeamID != project.TeamID || link.Role != "viewer" {
		t.Fatalf("organization team must have viewer access: %+v", link)
	}
	if err := store.db.Model(&link).Update("role", "maintainer").Error; err != nil {
		t.Fatal(err)
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_project_team_replay", "project.updated", "project", "project_team", "tenant_team", 1, map[string]interface{}{
		"externalId": "project_team", "name": "Project", "organizationExternalId": "org_team", "ownerExternalId": "principal_team",
	}))
	if err := store.db.First(&link, "project_id = ?", project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if link.Role != "viewer" {
		t.Fatalf("stale project replay did not restore viewer access: %+v", link)
	}

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_project_team_clear", "project.updated", "project", "project_team", "tenant_team", 2, map[string]interface{}{
		"externalId": "project_team", "name": "Project", "organizationExternalId": nil, "ownerExternalId": "principal_team",
	}))
	project, found = store.GetProject(projection.ID)
	if !found || project.TeamID != "" || project.OwnerUserID == "" {
		t.Fatalf("project organization was not cleared: %+v", project)
	}
	var count int64
	if err := store.db.Model(&ProjectTeam{}).Where("project_id = ?", project.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("removed organization retained %d project team links", count)
	}
}
