package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

type GatewayIntegrationDependencyReadiness struct {
	Ready                         bool   `json:"ready"`
	TenantID                      string `json:"tenant_id"`
	ProjectID                     string `json:"project_id,omitempty"`
	PrincipalType                 string `json:"principal_type"`
	PrincipalID                   string `json:"principal_id"`
	TenantVersion                 int64  `json:"tenant_version,omitempty"`
	PrincipalVersion              int64  `json:"principal_version,omitempty"`
	ProjectVersion                int64  `json:"project_version,omitempty"`
	OrganizationMembershipVersion int64  `json:"organization_membership_version,omitempty"`
	DependencyCode                string `json:"dependency_code,omitempty"`
}

type GatewayIntegrationDependencyInput struct {
	GatewayModelAccessKeyCreateInput
	MinimumTenantVersion                 int64
	MinimumPrincipalVersion              int64
	MinimumProjectVersion                int64
	MinimumOrganizationMembershipVersion int64
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
	minimumProjectVersion, err := optionalPositiveProjectionVersion(r.URL.Query().Get("project_version"))
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_integration_dependency_query", "project_version must be a positive integer"))
		return
	}
	minimumOrganizationMembershipVersion, err := optionalPositiveProjectionVersion(r.URL.Query().Get("organization_membership_version"))
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_integration_dependency_query", "organization_membership_version must be a positive integer"))
		return
	}
	input := GatewayIntegrationDependencyInput{
		GatewayModelAccessKeyCreateInput: GatewayModelAccessKeyCreateInput{
			TenantExternalID:    strings.TrimSpace(r.URL.Query().Get("tenant_id")),
			ProjectExternalID:   strings.TrimSpace(r.URL.Query().Get("project_id")),
			PrincipalType:       r.URL.Query().Get("principal_type"),
			PrincipalExternalID: strings.TrimSpace(r.URL.Query().Get("principal_id")),
		},
		MinimumTenantVersion: minimumTenantVersion, MinimumPrincipalVersion: minimumPrincipalVersion,
		MinimumProjectVersion:                minimumProjectVersion,
		MinimumOrganizationMembershipVersion: minimumOrganizationMembershipVersion,
	}
	input, err = normalizeGatewayIntegrationDependencyInput(input)
	if err != nil {
		writeError(w, r, err)
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

func normalizeGatewayIntegrationDependencyInput(input GatewayIntegrationDependencyInput) (GatewayIntegrationDependencyInput, error) {
	input.TenantExternalID = strings.TrimSpace(input.TenantExternalID)
	input.ProjectExternalID = strings.TrimSpace(input.ProjectExternalID)
	var principalTypeSupported bool
	input.PrincipalType, principalTypeSupported = normalizeGatewayPrincipalType(input.PrincipalType)
	input.PrincipalExternalID = strings.TrimSpace(input.PrincipalExternalID)
	if input.TenantExternalID == "" || input.PrincipalType == "" || input.PrincipalExternalID == "" ||
		(input.ProjectExternalID == "" && input.PrincipalType != "user") {
		return GatewayIntegrationDependencyInput{}, NewHTTPError(http.StatusBadRequest, "invalid_integration_dependency_query", "tenant_id, principal_type, and principal_id are required; project_id is required for non-user principals")
	}
	if !principalTypeSupported {
		return GatewayIntegrationDependencyInput{}, NewHTTPError(http.StatusBadRequest, "invalid_integration_dependency_query", "principal_type is not supported")
	}
	if (input.ProjectExternalID == "" && (input.MinimumProjectVersion > 0 || input.MinimumOrganizationMembershipVersion > 0)) ||
		(input.MinimumOrganizationMembershipVersion > 0 && input.PrincipalType != "user") {
		return GatewayIntegrationDependencyInput{}, NewHTTPError(http.StatusBadRequest, "invalid_integration_dependency_query", "Project and organization membership versions require a compatible project-scoped dependency")
	}
	return input, nil
}

func (s *GormStore) CheckGatewayIntegrationDependencies(input GatewayIntegrationDependencyInput) (GatewayIntegrationDependencyReadiness, error) {
	var err error
	input, err = normalizeGatewayIntegrationDependencyInput(input)
	readiness := GatewayIntegrationDependencyReadiness{
		TenantID: input.TenantExternalID, ProjectID: input.ProjectExternalID,
		PrincipalType: input.PrincipalType, PrincipalID: input.PrincipalExternalID,
	}
	if err != nil {
		readiness.DependencyCode = httpErrorCode(err)
		return readiness, err
	}
	err = s.withReadSnapshot(func(tx *gorm.DB) error {
		if input.ProjectExternalID != "" {
			tenant, project, err := resolveGatewayModelAccessKeyScope(tx, input.GatewayModelAccessKeyCreateInput)
			if err != nil {
				return err
			}
			if tenant.Version < input.MinimumTenantVersion {
				readiness.TenantVersion = tenant.Version
				return NewHTTPError(http.StatusConflict, "gateway_tenant_stale", "Gateway tenant projection has not reached the required version")
			}
			if input.MinimumTenantVersion > 0 {
				readiness.TenantVersion = tenant.Version
			}
			if input.MinimumProjectVersion > 0 {
				readiness.ProjectVersion = project.Version
				if project.Version < input.MinimumProjectVersion {
					return NewHTTPError(http.StatusConflict, "gateway_project_stale", "Gateway project projection has not reached the required version")
				}
			}
			if input.MinimumOrganizationMembershipVersion > 0 && project.OrganizationID == "" {
				return NewHTTPError(http.StatusBadRequest, "invalid_integration_dependency_query", "organization_membership_version requires a project organization")
			}
			if err := validateGatewayModelAccessKeyPrincipal(tx, tenant, project, input.GatewayModelAccessKeyCreateInput); err != nil {
				return err
			}
			if input.MinimumPrincipalVersion > 0 {
				if input.PrincipalType == "user" {
					var principal GatewayPrincipal
					if err := tx.First(&principal, "tenant_id = ? AND external_principal_id = ? AND status = ? AND deleted_at IS NULL", tenant.ID, input.PrincipalExternalID, StatusActive).Error; err != nil {
						if errors.Is(err, gorm.ErrRecordNotFound) {
							return NewHTTPError(http.StatusConflict, "gateway_principal_stale", "Gateway principal projection has not reached the required version")
						}
						return err
					}
					readiness.PrincipalVersion = principal.Version
					if principal.Version < input.MinimumPrincipalVersion {
						return NewHTTPError(http.StatusConflict, "gateway_principal_stale", "Gateway principal projection has not reached the required version")
					}
				} else {
					var workload GatewayWorkload
					if err := tx.First(&workload, "tenant_id = ? AND external_workload_id = ? AND project_id = ? AND workload_type = ? AND status = ? AND deleted_at IS NULL", tenant.ID, input.PrincipalExternalID, project.ID, input.PrincipalType, StatusActive).Error; err != nil {
						if errors.Is(err, gorm.ErrRecordNotFound) {
							return NewHTTPError(http.StatusConflict, "gateway_workload_stale", "Gateway workload projection has not reached the required version")
						}
						return err
					}
					readiness.PrincipalVersion = workload.Version
					if workload.Version < input.MinimumPrincipalVersion {
						return NewHTTPError(http.StatusConflict, "gateway_workload_stale", "Gateway workload projection has not reached the required version")
					}
				}
			}
			if input.MinimumOrganizationMembershipVersion > 0 && input.PrincipalType == "user" && project.OrganizationID != "" {
				var principal GatewayPrincipal
				if err := tx.First(&principal, "tenant_id = ? AND external_principal_id = ? AND status = ? AND deleted_at IS NULL", tenant.ID, input.PrincipalExternalID, StatusActive).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return NewHTTPError(http.StatusConflict, "gateway_organization_membership_stale", "Gateway organization membership projection has not reached the required version")
					}
					return err
				}
				var binding GatewayPrincipalOrganizationBinding
				if err := tx.Where("tenant_id = ? AND principal_id = ? AND organization_id = ? AND status = ? AND deleted_at IS NULL", tenant.ID, principal.ID, project.OrganizationID, StatusActive).
					Order("version DESC, id ASC").First(&binding).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return NewHTTPError(http.StatusConflict, "gateway_organization_membership_stale", "Gateway organization membership projection has not reached the required version")
					}
					return err
				}
				readiness.OrganizationMembershipVersion = binding.Version
				if binding.Version < input.MinimumOrganizationMembershipVersion {
					return NewHTTPError(http.StatusConflict, "gateway_organization_membership_stale", "Gateway organization membership projection has not reached the required version")
				}
			}
		} else {
			var tenant GatewayTenant
			if err := tx.First(&tenant, "external_tenant_id = ? AND status = ? AND deleted_at IS NULL", input.TenantExternalID, StatusActive).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return NewHTTPError(http.StatusConflict, "gateway_tenant_unavailable", "Gateway tenant projection is not active")
				}
				return err
			}
			if tenant.Version < input.MinimumTenantVersion {
				readiness.TenantVersion = tenant.Version
				return NewHTTPError(http.StatusConflict, "gateway_tenant_stale", "Gateway tenant projection has not reached the required version")
			}
			if input.MinimumTenantVersion > 0 {
				readiness.TenantVersion = tenant.Version
			}
			var principal GatewayPrincipal
			if err := tx.First(&principal, "tenant_id = ? AND external_principal_id = ? AND status = ? AND deleted_at IS NULL", tenant.ID, input.PrincipalExternalID, StatusActive).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return NewHTTPError(http.StatusConflict, "gateway_principal_unavailable", "Gateway principal projection is not active")
				}
				return err
			}
			if principal.Version < input.MinimumPrincipalVersion {
				readiness.PrincipalVersion = principal.Version
				return NewHTTPError(http.StatusConflict, "gateway_principal_stale", "Gateway principal projection has not reached the required version")
			}
			if input.MinimumPrincipalVersion > 0 {
				readiness.PrincipalVersion = principal.Version
			}
		}
		if input.PrincipalType == "user" {
			var managed GatewayManagedUser
			if err := tx.First(&managed, "external_principal_id = ?", input.PrincipalExternalID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return NewHTTPError(http.StatusConflict, "gateway_user_unavailable", "Managed gateway user is not available")
				}
				return err
			}
			var user AdminUser
			if err := tx.First(&user, "id = ? AND status = ?", managed.AdminUserID, StatusActive).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return NewHTTPError(http.StatusConflict, "gateway_user_unavailable", "Managed gateway user is not active")
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		readiness.DependencyCode = httpErrorCode(err)
		if AsHTTPError(err).Status == http.StatusConflict {
			return readiness, NewHTTPError(http.StatusConflict, "integration_dependency_pending", "Required integration projection is not ready")
		}
		return readiness, err
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
