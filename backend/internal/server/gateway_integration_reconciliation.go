package server

import (
	"errors"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

func (s *GormStore) lockGatewayManagedUsersForIntegrationEvent(tx *gorm.DB, event GatewayIntegrationEvent) error {
	if s.dbDriver != "postgres" {
		return nil
	}
	principalIDs, err := gatewayManagedUserIDsForIntegrationEvent(tx, event)
	if err != nil {
		return err
	}
	for _, principalID := range principalIDs {
		if err := s.lockScopeForUpdate(tx, "gateway_managed_user", principalID); err != nil {
			return err
		}
	}
	return nil
}

// Include soft-deleted projections so replay and reactivation events lock the
// same managed users as their active counterparts before applying a change.
func gatewayManagedUserIDsForIntegrationEvent(tx *gorm.DB, event GatewayIntegrationEvent) ([]string, error) {
	principalIDs := make([]string, 0)
	switch event.AggregateType {
	case "tenant_member":
		principalIDs = append(principalIDs, payloadString(event.Payload, "principalExternalId"))
	case "organization_member":
		principalIDs = append(principalIDs, payloadString(event.Payload, "principalExternalId"))
		var tenant GatewayTenant
		if err := tx.Unscoped().First(&tenant, "external_tenant_id = ?", event.TenantID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			break
		} else if err != nil {
			return nil, err
		}
		var binding GatewayPrincipalOrganizationBinding
		if err := tx.Unscoped().First(&binding, "tenant_id = ? AND external_membership_id = ?", tenant.ID, event.AggregateID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			break
		} else if err != nil {
			return nil, err
		}
		if binding.PrincipalID != "" {
			var previousPrincipal GatewayPrincipal
			if err := tx.Unscoped().First(&previousPrincipal, "id = ?", binding.PrincipalID).Error; err != nil {
				return nil, err
			}
			principalIDs = append(principalIDs, previousPrincipal.ExternalPrincipalID)
		}
	case "tenant":
		var tenant GatewayTenant
		if err := tx.Unscoped().First(&tenant, "external_tenant_id = ?", event.TenantID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		if err := tx.Unscoped().Model(&GatewayPrincipal{}).Where("tenant_id = ?", tenant.ID).
			Pluck("external_principal_id", &principalIDs).Error; err != nil {
			return nil, err
		}
	case "organization":
		var tenant GatewayTenant
		if err := tx.Unscoped().First(&tenant, "external_tenant_id = ?", event.TenantID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		var organization GatewayOrganization
		if err := tx.Unscoped().First(&organization, "tenant_id = ? AND external_organization_id = ?", tenant.ID, event.AggregateID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		if err := tx.Unscoped().Table("gateway_principal_organization_bindings AS gob").
			Distinct("gpr.external_principal_id").
			Joins("JOIN gateway_principals AS gpr ON gpr.id = gob.principal_id").
			Where("gob.organization_id = ?", organization.ID).
			Pluck("gpr.external_principal_id", &principalIDs).Error; err != nil {
			return nil, err
		}
	default:
		return principalIDs, nil
	}

	sort.Strings(principalIDs)
	result := principalIDs[:0]
	previous := ""
	for _, principalID := range principalIDs {
		principalID = strings.TrimSpace(principalID)
		if principalID == "" || principalID == previous {
			continue
		}
		result = append(result, principalID)
		previous = principalID
	}
	return result, nil
}

func syncGatewayServingTenantResources(tx *gorm.DB, tenant GatewayTenant, now time.Time) error {
	var organizations []GatewayOrganization
	if err := tx.Where("tenant_id = ?", tenant.ID).Find(&organizations).Error; err != nil {
		return err
	}
	for _, organization := range organizations {
		if _, err := syncGatewayServingOrganizationTeam(tx, organization, now); err != nil {
			return err
		}
	}
	var projects []GatewayProject
	if err := tx.Where("tenant_id = ?", tenant.ID).Find(&projects).Error; err != nil {
		return err
	}
	for _, project := range projects {
		if err := syncGatewayServingProject(tx, project, now); err != nil {
			return err
		}
	}
	return nil
}

func syncGatewayServingProjectsForOrganization(tx *gorm.DB, organizationID string, now time.Time) error {
	var projects []GatewayProject
	if err := tx.Where("organization_id = ?", organizationID).Find(&projects).Error; err != nil {
		return err
	}
	for _, project := range projects {
		if err := syncGatewayServingProject(tx, project, now); err != nil {
			return err
		}
	}
	return nil
}
