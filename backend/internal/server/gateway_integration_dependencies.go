package server

import (
	"net/http"
	"strconv"
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

type GatewayIntegrationDependencyInput struct {
	GatewayModelAccessKeyCreateInput
	MinimumTenantVersion    int64
	MinimumPrincipalVersion int64
}

func (s *Server) handleGatewayIntegrationDependencies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}
	minimumTenantVersion, err := optionalPositiveProjectionVersion(r.URL.Query().Get("tenant_version"))
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_integration_dependency_query", "tenant_version must be a positive integer"))
		return
	}
	minimumPrincipalVersion, err := optionalPositiveProjectionVersion(r.URL.Query().Get("principal_version"))
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_integration_dependency_query", "principal_version must be a positive integer"))
		return
	}
	input := GatewayIntegrationDependencyInput{
		GatewayModelAccessKeyCreateInput: GatewayModelAccessKeyCreateInput{
			TenantExternalID:    strings.TrimSpace(r.URL.Query().Get("tenant_id")),
			ProjectExternalID:   strings.TrimSpace(r.URL.Query().Get("project_id")),
			PrincipalType:       strings.TrimSpace(r.URL.Query().Get("principal_type")),
			PrincipalExternalID: strings.TrimSpace(r.URL.Query().Get("principal_id")),
		},
		MinimumTenantVersion: minimumTenantVersion, MinimumPrincipalVersion: minimumPrincipalVersion,
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

func optionalPositiveProjectionVersion(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version < 1 {
		return 0, NewHTTPError(http.StatusBadRequest, "invalid_integration_dependency_query", "Projection version must be a positive integer")
	}
	return version, nil
}

func (s *GormStore) CheckGatewayIntegrationDependencies(input GatewayIntegrationDependencyInput) (GatewayIntegrationDependencyReadiness, error) {
	readiness := GatewayIntegrationDependencyReadiness{
		TenantID: input.TenantExternalID, ProjectID: input.ProjectExternalID,
		PrincipalType: input.PrincipalType, PrincipalID: input.PrincipalExternalID,
	}
	err := s.withReadSnapshot(func(tx *gorm.DB) error {
		if input.ProjectExternalID != "" {
			tenant, project, err := resolveGatewayModelAccessKeyScope(tx, input.GatewayModelAccessKeyCreateInput)
			if err != nil {
				return err
			}
			if tenant.Version < input.MinimumTenantVersion {
				return NewHTTPError(http.StatusConflict, "gateway_tenant_stale", "Gateway tenant projection has not reached the required version")
			}
			if err := validateGatewayModelAccessKeyPrincipal(tx, tenant, project, input.GatewayModelAccessKeyCreateInput); err != nil {
				return err
			}
			if input.MinimumPrincipalVersion > 0 {
				if input.PrincipalType == "user" {
					var principal GatewayPrincipal
					if err := tx.First(&principal, "tenant_id = ? AND external_principal_id = ? AND status = ?", tenant.ID, input.PrincipalExternalID, StatusActive).Error; err != nil || principal.Version < input.MinimumPrincipalVersion {
						return NewHTTPError(http.StatusConflict, "gateway_principal_stale", "Gateway principal projection has not reached the required version")
					}
				} else {
					var workload GatewayWorkload
					if err := tx.First(&workload, "tenant_id = ? AND external_workload_id = ? AND project_id = ? AND workload_type = ? AND status = ?", tenant.ID, input.PrincipalExternalID, project.ID, input.PrincipalType, StatusActive).Error; err != nil || workload.Version < input.MinimumPrincipalVersion {
						return NewHTTPError(http.StatusConflict, "gateway_workload_stale", "Gateway workload projection has not reached the required version")
					}
				}
			}
		} else {
			var tenant GatewayTenant
			if err := tx.First(&tenant, "external_tenant_id = ? AND status = ?", input.TenantExternalID, StatusActive).Error; err != nil {
				return NewHTTPError(http.StatusConflict, "gateway_tenant_unavailable", "Gateway tenant projection is not active")
			}
			if tenant.Version < input.MinimumTenantVersion {
				return NewHTTPError(http.StatusConflict, "gateway_tenant_stale", "Gateway tenant projection has not reached the required version")
			}
			var principal GatewayPrincipal
			if err := tx.First(&principal, "tenant_id = ? AND external_principal_id = ? AND status = ?", tenant.ID, input.PrincipalExternalID, StatusActive).Error; err != nil {
				return NewHTTPError(http.StatusConflict, "gateway_principal_unavailable", "Gateway principal projection is not active")
			}
			if principal.Version < input.MinimumPrincipalVersion {
				return NewHTTPError(http.StatusConflict, "gateway_principal_stale", "Gateway principal projection has not reached the required version")
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
