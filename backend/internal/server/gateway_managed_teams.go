package server

import (
	"errors"
	"slices"
	"sort"
	"time"

	"gorm.io/gorm"
)

func gatewayManagedTeamIDs(tx *gorm.DB, teamIDs []string) (map[string]bool, error) {
	managedIDs := map[string]bool{}
	teamIDs = normalizedUniqueStrings(teamIDs)
	if len(teamIDs) == 0 {
		return managedIDs, nil
	}
	var teams []AdminResource
	if err := tx.Where("kind = ? AND id IN ?", "teams", teamIDs).Find(&teams).Error; err != nil {
		return nil, err
	}
	for _, team := range teams {
		if isGatewayManagedTeam(team) {
			managedIDs[team.ID] = true
		}
	}
	return managedIDs, nil
}

func refreshGatewayManagedUserTeams(tx *gorm.DB, externalPrincipalID string) error {
	var managed GatewayManagedUser
	if err := tx.First(&managed, "external_principal_id = ?", externalPrincipalID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	var user AdminUser
	if err := tx.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		return err
	}

	var principals []GatewayPrincipal
	if err := tx.Where("external_principal_id = ? AND status = ? AND deleted_at IS NULL", externalPrincipalID, StatusActive).Find(&principals).Error; err != nil {
		return err
	}
	managedIDs := map[string]bool{}
	primaryManagedIDs := map[string]bool{}
	now := time.Now().UTC()
	for _, principal := range principals {
		var tenant GatewayTenant
		if err := tx.Unscoped().First(&tenant, "id = ?", principal.TenantID).Error; err != nil {
			return err
		}
		if tenant.Status != StatusActive || tenant.DeletedAt != nil {
			continue
		}
		var bindings []GatewayPrincipalOrganizationBinding
		if err := tx.Where("principal_id = ? AND status = ? AND deleted_at IS NULL", principal.ID, StatusActive).Find(&bindings).Error; err != nil {
			return err
		}
		for _, binding := range bindings {
			var organization GatewayOrganization
			if err := tx.Unscoped().First(&organization, "id = ?", binding.OrganizationID).Error; err != nil {
				return err
			}
			if organization.Status != StatusActive || organization.DeletedAt != nil {
				continue
			}
			teamID, err := syncGatewayServingOrganizationTeam(tx, organization, now)
			if err != nil {
				return err
			}
			managedIDs[teamID] = true
			if binding.IsPrimary {
				primaryManagedIDs[teamID] = true
			}
		}
	}
	managedTeams := make([]string, 0, len(managedIDs))
	for teamID := range managedIDs {
		managedTeams = append(managedTeams, teamID)
	}
	sort.Strings(managedTeams)
	assignedManagedIDs, err := gatewayManagedTeamIDs(tx, append(slices.Clone(user.TeamIDs), user.TeamID))
	if err != nil {
		return err
	}
	localTeams := make([]string, 0, len(user.TeamIDs))
	for _, teamID := range user.TeamIDs {
		if !assignedManagedIDs[teamID] {
			localTeams = append(localTeams, teamID)
		}
	}
	primary := user.TeamID
	if assignedManagedIDs[primary] {
		primary = ""
	}
	if primary == "" {
		if len(primaryManagedIDs) == 1 {
			for teamID := range primaryManagedIDs {
				primary = teamID
			}
		} else if len(primaryManagedIDs) == 0 && len(managedTeams) == 1 && len(localTeams) == 0 {
			primary = managedTeams[0]
		}
	}
	teamIDs := normalizedTeamIDs(primary, append(localTeams, managedTeams...))
	if user.TeamID == primary && slices.Equal(user.TeamIDs, teamIDs) {
		return nil
	}
	user.TeamID, user.TeamIDs, user.UpdatedAt = primary, teamIDs, now
	return tx.Save(&user).Error
}

func refreshGatewayOrganizationTeamUsers(tx *gorm.DB, organizationID string) error {
	var externalPrincipalIDs []string
	if err := tx.Model(&GatewayPrincipalOrganizationBinding{}).
		Select("DISTINCT gateway_principals.external_principal_id").
		Joins("JOIN gateway_principals ON gateway_principals.id = gateway_principal_organization_bindings.principal_id").
		Where("gateway_principal_organization_bindings.organization_id = ?", organizationID).
		Where("gateway_principals.external_principal_id <> ''").
		Pluck("gateway_principals.external_principal_id", &externalPrincipalIDs).Error; err != nil {
		return err
	}
	for _, externalPrincipalID := range externalPrincipalIDs {
		if err := refreshGatewayManagedUserTeams(tx, externalPrincipalID); err != nil {
			return err
		}
	}
	return nil
}
