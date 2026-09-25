package server

import (
	"encoding/hex"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

func isGatewayManagedTeamID(teamID string) bool {
	if !strings.HasPrefix(teamID, "gwt_") || len(teamID) != 28 {
		return false
	}
	_, err := hex.DecodeString(teamID[4:])
	return err == nil
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
		if err := tx.First(&tenant, "id = ?", principal.TenantID).Error; err != nil {
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
			if err := tx.First(&organization, "id = ?", binding.OrganizationID).Error; err != nil {
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
	localTeams := make([]string, 0, len(user.TeamIDs))
	for _, teamID := range user.TeamIDs {
		if !isGatewayManagedTeamID(teamID) {
			localTeams = append(localTeams, teamID)
		}
	}
	primary := user.TeamID
	if isGatewayManagedTeamID(primary) {
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
	var bindings []GatewayPrincipalOrganizationBinding
	if err := tx.Where("organization_id = ?", organizationID).Find(&bindings).Error; err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, binding := range bindings {
		var principal GatewayPrincipal
		if err := tx.First(&principal, "id = ?", binding.PrincipalID).Error; err != nil {
			return err
		}
		if seen[principal.ExternalPrincipalID] {
			continue
		}
		seen[principal.ExternalPrincipalID] = true
		if err := refreshGatewayManagedUserTeams(tx, principal.ExternalPrincipalID); err != nil {
			return err
		}
	}
	return nil
}
