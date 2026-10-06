package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGatewayUsageAndLogsDoNotResolveForeignTenantProjectNames(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	now := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	for _, tenant := range []GatewayTenant{
		{ID: "gt_scope_a", ExternalTenantID: "tenant_scope_a", Name: "Tenant A", Status: StatusActive},
		{ID: "gt_scope_b", ExternalTenantID: "tenant_scope_b", Name: "Tenant B", Status: StatusActive},
	} {
		if err := store.db.Create(&tenant).Error; err != nil {
			t.Fatal(err)
		}
	}
	organization := GatewayOrganization{
		ID: "go_scope_b", TenantID: "gt_scope_b", ExternalOrganizationID: "org_scope_b",
		Name: "Foreign organization", Status: StatusActive,
	}
	if err := store.db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&GatewayProject{
		ID: "gwj_scope_b", TenantID: "gt_scope_b", ExternalProjectID: "project_scope_b",
		OrganizationID: organization.ID, Name: "Foreign project", Status: StatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&APIKey{
		ID: "key_scope_a", TenantExternalID: "tenant_scope_a", ProjectExternalID: "project_scope_a",
		ManagedBy: gatewayModelAccessKeyManagedBy, PrincipalType: "user", PrincipalExternalID: "principal_scope_a",
		Status: StatusActive, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestLog{
		ID: "log_scope_foreign_project", RequestID: "req_scope_foreign_project", ProjectID: "gwj_scope_b",
		APIKeyID: "key_scope_a", ModelName: "scope-model", StatusCode: http.StatusOK, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&UsageRecord{
		ID: "usage_scope_foreign_project", RequestID: "req_scope_foreign_project", ProjectID: "gwj_scope_b",
		APIKeyID: "key_scope_a", ModelName: "scope-model", TotalTokens: 7, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	logs := doJSON(t, app, http.MethodGet, "/api/internal/request-logs?tenant_id=tenant_scope_a", nil, "integration_token")
	if logs.Code != http.StatusOK {
		t.Fatalf("request log query failed: %d %s", logs.Code, logs.Body)
	}
	var logPayload struct {
		Data GatewayRequestLogPage `json:"data"`
	}
	if err := json.Unmarshal([]byte(logs.Body), &logPayload); err != nil {
		t.Fatal(err)
	}
	if len(logPayload.Data.Items) != 1 {
		t.Fatalf("unexpected request log page: %+v", logPayload.Data)
	}
	item := logPayload.Data.Items[0]
	if item.ProjectName != "" || item.OrganizationName != "" || item.OrganizationExternalID != "" {
		t.Fatalf("foreign project metadata crossed tenant boundary: %+v", item)
	}

	usage := doJSON(t, app, http.MethodGet, "/api/internal/usage?tenant_id=tenant_scope_a&date_from=2026-10-05T00:00:00Z&date_to=2026-10-05T23:59:59Z", nil, "integration_token")
	if usage.Code != http.StatusOK {
		t.Fatalf("usage query failed: %d %s", usage.Code, usage.Body)
	}
	if strings.Contains(usage.Body, "Foreign organization") || strings.Contains(usage.Body, "Foreign project") {
		t.Fatalf("foreign project metadata crossed tenant usage boundary: %s", usage.Body)
	}
	var usagePayload struct {
		Data GatewayUsageReport `json:"data"`
	}
	if err := json.Unmarshal([]byte(usage.Body), &usagePayload); err != nil {
		t.Fatal(err)
	}
	if usagePayload.Data.Summary.TotalTokens != 7 {
		t.Fatalf("usage row was incorrectly dropped with foreign project reference: %+v", usagePayload.Data.Summary)
	}
}
