package server

import (
	"net/http"
	"strings"

	"gorm.io/gorm"
)

type GatewayIntegrationDependencyReadiness struct {
	Ready          bool   `json:"ready"`
	TenantID       string `json:"tenant_id"`
	ProjectID      string `json:"project_id,omitempty"`
	PrincipalType  string `json:"principal_type"`
	PrincipalID    string `json:"principal_id"`
	DependencyCode string `json:"dependency_code,omitempty"`
}

func (s *Server) handleGatewayIntegrationDependencies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}
	input := GatewayModelAccessKeyCreateInput{
		TenantExternalID:    strings.TrimSpace(r.URL.Query().Get("tenant_id")),
		ProjectExternalID:   strings.TrimSpace(r.URL.Query().Get("project_id")),
		PrincipalType:       strings.TrimSpace(r.URL.Query().Get("principal_type")),
		PrincipalExternalID: strings.TrimSpace(r.URL.Query().Get("principal_id")),
	}
	if input.TenantExternalID == "" || input.PrincipalType == "" || input.PrincipalExternalID == "" || (input.ProjectExternalID == "" && input.PrincipalType != "user") {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_integration_dependency_query", "tenant_id, principal_type, and principal_id are required; project_id is required for non-user principals"))
		return
	}
	if !s.requireGatewayIntegrationTenant(w, r, input.TenantExternalID) {
		return
	}
	readiness, err := s.store.CheckGatewayIntegrationDependencies(input)
	if err != nil {
		if httpErr := AsHTTPError(err); httpErr.Code == "integration_dependency_pending" {
			writeJSON(w, http.StatusConflict, map[string]any{"data": readiness})
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": readiness})
}

func (s *GormStore) CheckGatewayIntegrationDependencies(input GatewayModelAccessKeyCreateInput) (GatewayIntegrationDependencyReadiness, error) {
	readiness := GatewayIntegrationDependencyReadiness{
		TenantID: input.TenantExternalID, ProjectID: input.ProjectExternalID,
		PrincipalType: input.PrincipalType, PrincipalID: input.PrincipalExternalID,
	}
	err := s.withReadSnapshot(func(tx *gorm.DB) error {
		if input.ProjectExternalID != "" {
			tenant, project, err := resolveGatewayModelAccessKeyScope(tx, input)
			if err != nil {
				return err
			}
			if err := validateGatewayModelAccessKeyPrincipal(tx, tenant, project, input); err != nil {
				return err
			}
		} else {
			var tenant GatewayTenant
			if err := tx.First(&tenant, "external_tenant_id = ? AND status = ?", input.TenantExternalID, StatusActive).Error; err != nil {
				return NewHTTPError(http.StatusConflict, "gateway_tenant_unavailable", "Gateway tenant projection is not active")
			}
			var principal GatewayPrincipal
			if err := tx.First(&principal, "tenant_id = ? AND external_principal_id = ? AND status = ?", tenant.ID, input.PrincipalExternalID, StatusActive).Error; err != nil {
				return NewHTTPError(http.StatusConflict, "gateway_principal_unavailable", "Gateway principal projection is not active")
			}
		}
		if input.PrincipalType == "user" {
			var managed GatewayManagedUser
			if err := tx.First(&managed, "external_principal_id = ?", input.PrincipalExternalID).Error; err != nil {
				return NewHTTPError(http.StatusConflict, "gateway_user_unavailable", "Managed gateway user is not available")
			}
			var user AdminUser
			if err := tx.First(&user, "id = ? AND status = ?", managed.AdminUserID, StatusActive).Error; err != nil {
				return NewHTTPError(http.StatusConflict, "gateway_user_unavailable", "Managed gateway user is not active")
			}
		}
		return nil
	})
	if err != nil {
		readiness.DependencyCode = httpErrorCode(err)
		return readiness, NewHTTPError(http.StatusConflict, "integration_dependency_pending", "Required integration projection is not ready")
	}
	readiness.Ready = true
	return readiness, nil
}

func httpErrorCode(err error) string {
	if httpErr := AsHTTPError(err); httpErr != nil {
		return httpErr.Code
	}
	return "integration_dependency_pending"
}
