package server

import "testing"

func TestGatewayOrganizationCostCenterUpdatesServingTeam(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_team_cc_tenant", "tenant.created", "tenant", "tenant_team_cc", "tenant_team_cc", 1, map[string]interface{}{
			"externalId": "tenant_team_cc", "name": "Cost center tenant",
		}),
		gatewayIntegrationEvent("evt_team_cc_org", "organization.created", "organization", "org_team_cc", "tenant_team_cc", 1, map[string]interface{}{
			"externalId": "org_team_cc", "name": "Engineering",
		}),
		gatewayIntegrationEvent("evt_team_cc_center", "cost_center.created", "cost_center", "cc_team", "tenant_team_cc", 1, map[string]interface{}{
			"externalId": "cc_team", "code": "CC-TEAM", "name": "Team cost center",
		}),
		gatewayIntegrationEvent("evt_team_cc_binding", "organization_cost_center.created", "organization_cost_center", "binding_team_cc", "tenant_team_cc", 1, map[string]interface{}{
			"externalId": "binding_team_cc", "organizationExternalId": "org_team_cc", "costCenterExternalId": "cc_team",
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}

	var initialTeam AdminResource
	if err := store.db.First(&initialTeam, "kind = ? AND name = ?", "teams", "Engineering").Error; err != nil {
		t.Fatal(err)
	}
	teamID := initialTeam.ID
	assertTeamCostCenter := func(want string) {
		t.Helper()
		var team AdminResource
		if err := store.db.First(&team, "kind = ? AND id = ?", "teams", teamID).Error; err != nil {
			t.Fatal(err)
		}
		if got := stringField(team.Fields, "cost_center"); got != want {
			t.Fatalf("expected serving team cost center %q, got %q in %+v", want, got, team.Fields)
		}
	}
	assertTeamCostCenter("CC-TEAM")

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_cc_rename", "cost_center.updated", "cost_center", "cc_team", "tenant_team_cc", 2, map[string]interface{}{
		"externalId": "cc_team", "code": "CC-TEAM-2", "status": StatusActive,
	}))
	assertTeamCostCenter("CC-TEAM-2")

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_cc_disable", "cost_center.disabled", "cost_center", "cc_team", "tenant_team_cc", 3, map[string]interface{}{
		"externalId": "cc_team", "status": StatusDisabled,
	}))
	assertTeamCostCenter("")

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_cc_enable", "cost_center.updated", "cost_center", "cc_team", "tenant_team_cc", 4, map[string]interface{}{
		"externalId": "cc_team", "status": StatusActive,
	}))
	assertTeamCostCenter("CC-TEAM-2")

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_team_cc_remove", "organization_cost_center.removed", "organization_cost_center", "binding_team_cc", "tenant_team_cc", 2, map[string]interface{}{
		"externalId": "binding_team_cc", "organizationExternalId": "org_team_cc", "costCenterExternalId": "cc_team", "status": "removed",
	}))
	assertTeamCostCenter("")
}
