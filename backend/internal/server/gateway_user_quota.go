package server

import (
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"
)

// handleGatewayUserQuota serves the self-service quota view used by Lumen.
// The external principal is resolved inside TokenHub and is never treated as
// the quota policy scope ID directly.
func (s *Server) handleGatewayUserQuota(w http.ResponseWriter, r *http.Request) {
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	tenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if !s.requireGatewayIntegrationTenant(w, r, tenantID) {
		return
	}
	principalID := strings.TrimSpace(r.URL.Query().Get("principal_id"))
	if principalID == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_principal_scope", "principal_id is required"))
		return
	}
	user, err := s.store.ResolveGatewayManagedQuotaUser(tenantID, principalID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	snapshot, err := s.userQuotaSnapshotForTenant(user, tenantID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": struct {
		PolicyConfigured bool             `json:"policy_configured"`
		Limits           QuotaLimits      `json:"limits"`
		Usage            QuotaPolicyUsage `json:"usage"`
	}{
		PolicyConfigured: snapshot.PolicyConfigured,
		Limits:           snapshot.Limits,
		Usage:            snapshot.Usage,
	}})
}

func (s *GormStore) ResolveGatewayManagedQuotaUser(tenantExternalID, principalExternalID string) (AdminUser, error) {
	tenantExternalID = strings.TrimSpace(tenantExternalID)
	principalExternalID = strings.TrimSpace(principalExternalID)
	if tenantExternalID == "" || principalExternalID == "" {
		return AdminUser{}, NewHTTPError(http.StatusBadRequest, "invalid_principal_scope", "Tenant and principal IDs are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var principal GatewayPrincipal
	if err := s.db.Joins("JOIN gateway_tenants ON gateway_tenants.id = gateway_principals.tenant_id").
		First(&principal, "gateway_principals.external_principal_id = ? AND gateway_tenants.external_tenant_id = ? AND gateway_principals.status = ? AND gateway_principals.deleted_at IS NULL AND gateway_tenants.status = ? AND gateway_tenants.deleted_at IS NULL", principalExternalID, tenantExternalID, StatusActive, StatusActive).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminUser{}, NewHTTPError(http.StatusForbidden, "gateway_membership_inactive", "Gateway tenant membership is not active")
		}
		return AdminUser{}, err
	}

	var managed GatewayManagedUser
	if err := s.db.First(&managed, "external_principal_id = ?", principal.ExternalPrincipalID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminUser{}, NewHTTPError(http.StatusConflict, "gateway_user_not_synced", "Gateway user has not been synchronized")
		}
		return AdminUser{}, err
	}
	var user AdminUser
	if err := s.db.First(&user, "id = ? AND status = ?", managed.AdminUserID, StatusActive).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminUser{}, NewHTTPError(http.StatusForbidden, "gateway_user_inactive", "Managed gateway user is not active")
		}
		return AdminUser{}, err
	}
	return publicAdminUser(user), nil
}

// ResolveAdminQuotaTenant binds the admin self-service quota view to an
// authenticated gateway membership. A caller may select a tenant only when
// the managed user has an active membership in that tenant; ambiguous
// multi-tenant sessions must not silently aggregate usage across tenants.
func (s *GormStore) ResolveAdminQuotaTenant(userID, requestedTenantID string) (string, error) {
	userID = strings.TrimSpace(userID)
	requestedTenantID = strings.TrimSpace(requestedTenantID)
	if userID == "" {
		return "", NewHTTPError(http.StatusUnauthorized, "invalid_admin_user", "Authenticated user is missing an ID")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var tenantIDs []string
	err := s.db.Table("gateway_tenants AS gt").
		Select("DISTINCT gt.external_tenant_id").
		Joins("JOIN gateway_principals AS gp ON gp.tenant_id = gt.id").
		Joins("JOIN gateway_managed_users AS gm ON gm.external_principal_id = gp.external_principal_id AND gm.admin_user_id = ?", userID).
		Where("gp.status = ? AND gp.deleted_at IS NULL AND gt.status = ? AND gt.deleted_at IS NULL AND gt.external_tenant_id <> ''", StatusActive, StatusActive).
		Order("gt.external_tenant_id ASC").Pluck("gt.external_tenant_id", &tenantIDs).Error
	if err != nil {
		return "", err
	}
	if requestedTenantID != "" {
		for _, tenantID := range tenantIDs {
			if tenantID == requestedTenantID {
				return requestedTenantID, nil
			}
		}
		return "", NewHTTPError(http.StatusForbidden, "tenant_context_forbidden", "Authenticated user is not an active member of the requested tenant")
	}
	switch len(tenantIDs) {
	case 0:
		// Local users and legacy records have no gateway tenant context. Their
		// historical unscoped quota remains readable without inventing one.
		return "", nil
	case 1:
		return tenantIDs[0], nil
	default:
		return "", NewHTTPError(http.StatusConflict, "tenant_context_required", "A tenant_id is required when the user belongs to multiple tenants")
	}
}
