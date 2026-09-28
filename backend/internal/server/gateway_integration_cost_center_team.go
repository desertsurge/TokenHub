package server

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

func applyGatewayServingTeamCostCenter(tx *gorm.DB, organization GatewayOrganization, team *AdminResource) error {
	delete(team.Fields, "cost_center")

	var binding GatewayOrganizationCostCenter
	err := tx.First(&binding,
		"tenant_id = ? AND organization_id = ? AND status = ? AND deleted_at IS NULL",
		organization.TenantID, organization.ID, StatusActive,
	).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	var costCenter GatewayCostCenter
	if err := tx.First(&costCenter, "id = ?", binding.CostCenterID).Error; err != nil {
		return err
	}
	if costCenter.Status != StatusActive || costCenter.DeletedAt != nil {
		return nil
	}

	value := costCenter.Code
	if value == "" {
		value = costCenter.ExternalCostCenterID
	}
	team.Fields["cost_center"] = value
	return nil
}

func syncGatewayServingTeamsForCostCenter(tx *gorm.DB, costCenter GatewayCostCenter, now time.Time) error {
	var bindings []GatewayOrganizationCostCenter
	if err := tx.Where("tenant_id = ? AND cost_center_id = ?", costCenter.TenantID, costCenter.ID).Find(&bindings).Error; err != nil {
		return err
	}

	organizationIDs := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		organizationIDs[binding.OrganizationID] = struct{}{}
	}
	for organizationID := range organizationIDs {
		var organization GatewayOrganization
		if err := tx.First(&organization, "id = ?", organizationID).Error; err != nil {
			return err
		}
		if _, err := syncGatewayServingOrganizationTeam(tx, organization, now); err != nil {
			return err
		}
	}
	return nil
}

func syncGatewayServingTeamForOrganizationID(tx *gorm.DB, organizationID string, now time.Time) error {
	if organizationID == "" {
		return nil
	}
	var organization GatewayOrganization
	if err := tx.First(&organization, "id = ?", organizationID).Error; err != nil {
		return err
	}
	_, err := syncGatewayServingOrganizationTeam(tx, organization, now)
	return err
}
