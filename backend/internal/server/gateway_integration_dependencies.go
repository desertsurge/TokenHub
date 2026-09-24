package server

import (
	"net/http"
	"strings"

	"gorm.io/gorm"
)

type GatewayIntegrationDependencyReadiness struct {
	Ready          bool   `json:"ready"`
	TenantID       string `json:"tenant_id"`
	ProjectID      string `json:"project_id"`
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
	if input.TenantExternalID == "" || input.ProjectExternalID == "" || input.PrincipalType == "" || input.PrincipalExternalID == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_integration_dependency_query", "tenant_id, project_id, principal_type, and principal_id are required"))
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
		tenant, project, err := resolveGatewayModelAccessKeyScope(tx, input)
		if err != nil {
			return err
		}
		return validateGatewayModelAccessKeyPrincipal(tx, tenant, project, input)
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
