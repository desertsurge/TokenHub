package server

import (
	"errors"
	"net/http"

	"gorm.io/gorm"
)

func requireGatewayProjectOwner(tx *gorm.DB, tenant GatewayTenant, externalPrincipalID string) (GatewayPrincipal, error) {
	var principal GatewayPrincipal
	err := tx.First(&principal,
		"tenant_id = ? AND external_principal_id = ? AND status = ? AND deleted_at IS NULL",
		tenant.ID, externalPrincipalID, StatusActive,
	).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return GatewayPrincipal{}, NewHTTPError(http.StatusConflict, "integration_dependency_missing", "Gateway project owner projection is not active")
	}
	if err != nil {
		return GatewayPrincipal{}, err
	}

	ownerUserID, ready, err := gatewayServingProjectOwner(tx, GatewayProject{TenantID: tenant.ID, OwnerPrincipalID: principal.ID})
	if err != nil {
		return GatewayPrincipal{}, err
	}
	if !ready || ownerUserID == "" {
		return GatewayPrincipal{}, NewHTTPError(http.StatusConflict, "integration_dependency_missing", "Gateway project owner account is not available")
	}
	return principal, nil
}

func gatewayServingProjectOwner(tx *gorm.DB, projection GatewayProject) (string, bool, error) {
	if projection.OwnerPrincipalID == "" {
		return "", true, nil
	}

	var principal GatewayPrincipal
	err := tx.First(&principal,
		"id = ? AND tenant_id = ? AND status = ? AND deleted_at IS NULL",
		projection.OwnerPrincipalID, projection.TenantID, StatusActive,
	).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	var managed GatewayManagedUser
	err = tx.First(&managed, "external_principal_id = ?", principal.ExternalPrincipalID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	var user AdminUser
	err = tx.First(&user, "id = ? AND status = ?", managed.AdminUserID, StatusActive).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return user.ID, true, nil
}
