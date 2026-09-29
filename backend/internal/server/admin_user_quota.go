package server

import (
	"net/http"
	"strings"
)

func (s *Server) handleAdminUserQuotaGet(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}

	tenantExternalID, err := s.store.ResolveAdminQuotaTenant(user.ID, r.URL.Query().Get("tenant_id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	snapshot, err := s.userQuotaSnapshotForTenant(user, tenantExternalID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) userQuotaSnapshot(user AdminUser) (UserQuotaSnapshot, error) {
	return s.userQuotaSnapshotForTenant(user, "")
}

func (s *Server) userQuotaSnapshotForTenant(user AdminUser, tenantExternalID string) (UserQuotaSnapshot, error) {
	userID := strings.TrimSpace(user.ID)
	if userID == "" {
		return UserQuotaSnapshot{}, NewHTTPError(http.StatusUnauthorized, "invalid_admin_user", "Authenticated user is missing an ID")
	}

	limits := QuotaLimits{}
	policyConfigured := false
	resources, err := s.store.ListResourcesChecked("quota-policies")
	if err != nil {
		return UserQuotaSnapshot{}, err
	}
	for _, resource := range resources {
		if resource.Status != StatusActive {
			continue
		}
		scope := strings.ToLower(strings.TrimSpace(firstStringField(resource.Fields, "scope", "scope_type")))
		if scope != "user" || strings.TrimSpace(firstStringField(resource.Fields, "scope_id")) != userID {
			continue
		}
		policyTenantID := strings.TrimSpace(firstStringField(resource.Fields, "tenant_id", "tenant_external_id"))
		if tenantExternalID != "" && policyTenantID != "" && policyTenantID != tenantExternalID {
			continue
		}
		policyConfigured = true
		limits = mergeQuotaLimits(limits, quotaLimitsFromFields(resource.Fields))
	}

	usage, _, err := s.store.GetQuotaPolicyUsage("user", userID, tenantExternalID)
	if err != nil {
		return UserQuotaSnapshot{}, err
	}
	return UserQuotaSnapshot{
		UserID:           userID,
		PolicyConfigured: policyConfigured,
		Limits:           limits,
		Usage:            usage,
	}, nil
}
