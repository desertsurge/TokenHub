package server

import (
	"net/http"
	"testing"
)

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

func TestGatewayOrganizationRejectsLocalTeamIDCollision(t *testing.T) {
	store := NewMemoryStore()
	config := Config{AdminToken: "admin_token", IntegrationToken: "integration_token", SecretKey: "test_secret"}
	app := NewWithConfig(store, config).Handler()
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_collision_tenant", "tenant.created", "tenant", "tenant_team_collision", "tenant_team_collision", 1, map[string]interface{}{
		"externalId": "tenant_team_collision", "name": "Team collision tenant",
	}))
	teamID := gatewayServingOrganizationTeamID("tenant_team_collision", "org_team_collision")
	created := doJSON(t, app, http.MethodPost, "/api/admin/resources/teams", map[string]interface{}{
		"id": teamID, "name": "Existing local team",
	}, config.AdminToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create colliding local team: status=%d body=%s", created.Code, created.Body)
	}

	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", gatewayIntegrationEvent(
		"evt_team_collision_org", "organization.created", "organization", "org_team_collision", "tenant_team_collision", 1,
		map[string]interface{}{"externalId": "org_team_collision", "name": "Managed organization"},
	), config.IntegrationToken)
	if response.Code != http.StatusConflict || !jsonBodyHasCode(response.Body, "integration_team_conflict") {
		t.Fatalf("expected local team ID collision rejection, got %d: %s", response.Code, response.Body)
	}
	var team AdminResource
	if err := store.db.First(&team, "kind = ? AND id = ?", "teams", teamID).Error; err != nil {
		t.Fatal(err)
	}
	if team.Name != "Existing local team" || isGatewayManagedTeam(team) {
		t.Fatalf("organization sync took over a local team: %+v", team)
	}
	var organizationCount int64
	if err := store.db.Model(&GatewayOrganization{}).Where("external_organization_id = ?", "org_team_collision").Count(&organizationCount).Error; err != nil {
		t.Fatal(err)
	}
	if organizationCount != 0 {
		t.Fatalf("rejected organization event persisted %d projections", organizationCount)
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

func TestGatewayTenantAndOrganizationLifecycleUpdatesServingResources(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_lifecycle_tenant", "tenant.created", "tenant", "tenant_lifecycle", "tenant_lifecycle", 1, map[string]interface{}{
			"externalId": "tenant_lifecycle", "name": "Lifecycle tenant",
		}),
		gatewayIntegrationEvent("evt_lifecycle_org", "organization.created", "organization", "org_lifecycle", "tenant_lifecycle", 1, map[string]interface{}{
			"externalId": "org_lifecycle", "name": "Lifecycle organization",
		}),
		gatewayIntegrationEvent("evt_lifecycle_project", "project.created", "project", "project_lifecycle", "tenant_lifecycle", 1, map[string]interface{}{
			"externalId": "project_lifecycle", "name": "Lifecycle project", "organizationExternalId": "org_lifecycle",
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}

	var projection GatewayProject
	if err := store.db.First(&projection, "external_project_id = ?", "project_lifecycle").Error; err != nil {
		t.Fatal(err)
	}
	project, found := store.GetProject(projection.ID)
	if !found || project.Status != StatusActive || project.TeamID == "" {
		t.Fatalf("expected active serving project, found=%v project=%+v", found, project)
	}
	teamID := project.TeamID
	assertStatuses := func(want string) {
		t.Helper()
		project, found = store.GetProject(projection.ID)
		if !found || project.Status != want {
			t.Fatalf("expected serving project status %q, found=%v project=%+v", want, found, project)
		}
		var team AdminResource
		if err := store.db.First(&team, "kind = ? AND id = ?", "teams", teamID).Error; err != nil {
			t.Fatal(err)
		}
		if team.Status != want {
			t.Fatalf("expected serving team status %q, got %+v", want, team)
		}
	}

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_lifecycle_tenant_disabled", "tenant.disabled", "tenant", "tenant_lifecycle", "tenant_lifecycle", 2, map[string]interface{}{
		"externalId": "tenant_lifecycle", "status": StatusDisabled,
	}))
	assertStatuses(StatusDisabled)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_lifecycle_tenant_active", "tenant.updated", "tenant", "tenant_lifecycle", "tenant_lifecycle", 3, map[string]interface{}{
		"externalId": "tenant_lifecycle", "status": StatusActive,
	}))
	assertStatuses(StatusActive)
	if err := store.db.Model(&Project{}).Where("id = ?", projection.ID).Update("status", StatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&AdminResource{}).Where("kind = ? AND id = ?", "teams", teamID).Update("status", StatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_lifecycle_tenant_replay", "tenant.updated", "tenant", "tenant_lifecycle", "tenant_lifecycle", 3, map[string]interface{}{
		"externalId": "tenant_lifecycle", "status": StatusActive,
	}))
	assertStatuses(StatusActive)

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_lifecycle_org_disabled", "organization.disabled", "organization", "org_lifecycle", "tenant_lifecycle", 2, map[string]interface{}{
		"externalId": "org_lifecycle", "status": StatusDisabled,
	}))
	assertStatuses(StatusDisabled)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_lifecycle_org_active", "organization.updated", "organization", "org_lifecycle", "tenant_lifecycle", 3, map[string]interface{}{
		"externalId": "org_lifecycle", "status": StatusActive,
	}))
	assertStatuses(StatusActive)
	if err := store.db.Model(&Project{}).Where("id = ?", projection.ID).Update("status", StatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_lifecycle_org_replay", "organization.updated", "organization", "org_lifecycle", "tenant_lifecycle", 3, map[string]interface{}{
		"externalId": "org_lifecycle", "status": StatusActive,
	}))
	assertStatuses(StatusActive)
}

func TestGatewayManagedTeamRejectsLocalMutations(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{AdminToken: "admin_token", IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_read_only_tenant", "tenant.created", "tenant", "tenant_read_only", "tenant_read_only", 1, map[string]interface{}{
		"externalId": "tenant_read_only", "name": "Read-only tenant",
	}))
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_read_only_org", "organization.created", "organization", "org_read_only", "tenant_read_only", 1, map[string]interface{}{
		"externalId": "org_read_only", "name": "Read-only organization",
	}))

	var team AdminResource
	if err := store.db.First(&team, "kind = ? AND name = ?", "teams", "Read-only organization").Error; err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   map[string]interface{}
	}{
		{name: "replace", method: http.MethodPost, path: "/api/admin/resources/teams", body: map[string]interface{}{"id": team.ID, "name": "Local replacement"}},
		{name: "update", method: http.MethodPatch, path: "/api/admin/resources/teams/" + team.ID, body: map[string]interface{}{"name": "Local update"}},
		{name: "delete", method: http.MethodDelete, path: "/api/admin/resources/teams/" + team.ID},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := doJSON(t, app, test.method, test.path, test.body, "admin_token")
			if response.Code != http.StatusConflict || !jsonBodyHasCode(response.Body, "integration_managed_team_read_only") {
				t.Fatalf("expected managed team mutation rejection, got %d: %s", response.Code, response.Body)
			}
		})
	}

	var persisted AdminResource
	if err := store.db.First(&persisted, "kind = ? AND id = ?", "teams", team.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Name != "Read-only organization" || persisted.Status != StatusActive {
		t.Fatalf("local mutation changed the managed team: %+v", persisted)
	}
}
