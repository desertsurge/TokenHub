package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPaginateGatewayProjectionDetailsBoundsEachAggregate(t *testing.T) {
	detail := func(id string) gatewayProjectionDigestItem {
		return gatewayProjectionDigestItem{ExternalID: id, Status: StatusActive, Version: 1}
	}
	summary := GatewayIntegrationReconciliationSummary{ProjectionDetails: map[string][]gatewayProjectionDigestItem{
		"organization": {detail("organization-1"), detail("organization-2"), detail("organization-3")},
		"tenant":       {detail("tenant-1")},
	}}

	paginateGatewayProjectionDetails(&summary, 2, 2)

	if summary.ProjectionDetailsPage != 2 || summary.ProjectionDetailsPageSize != 2 {
		t.Fatalf("unexpected details pagination metadata: %+v", summary)
	}
	if summary.ProjectionDetailTotals["organization"] != 3 || summary.ProjectionDetailTotals["tenant"] != 1 {
		t.Fatalf("unexpected details totals: %+v", summary.ProjectionDetailTotals)
	}
	if got := summary.ProjectionDetails["organization"]; len(got) != 1 || got[0].ExternalID != "organization-3" {
		t.Fatalf("unexpected organization page: %+v", got)
	}
	if got := summary.ProjectionDetails["tenant"]; len(got) != 0 || got == nil {
		t.Fatalf("out-of-range tenant page must be an empty array: %+v", got)
	}
}

func TestGatewayReconciliationDetailsPaginationContract(t *testing.T) {
	app := NewWithConfig(NewMemoryStore(), Config{IntegrationToken: "integration-token", SecretKey: "test-secret"}).Handler()
	response := doJSON(t, app, http.MethodGet, "/api/internal/integration/reconciliation?tenant_id=tenant-page&include_details=1", nil, "integration-token")
	if response.Code != http.StatusOK {
		t.Fatalf("get paginated reconciliation details: %d %s", response.Code, response.Body)
	}
	var summary GatewayIntegrationReconciliationSummary
	if err := json.Unmarshal([]byte(response.Body), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.ProjectionDetailsPage != 1 || summary.ProjectionDetailsPageSize != 100 {
		t.Fatalf("unexpected default details pagination: %+v", summary)
	}
	if len(summary.ProjectionDetailTotals) != 8 {
		t.Fatalf("missing per-aggregate detail totals: %+v", summary.ProjectionDetailTotals)
	}

	invalid := doJSON(t, app, http.MethodGet, "/api/internal/integration/reconciliation?tenant_id=tenant-page&include_details=1&details_page_size=101", nil, "integration-token")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body, "invalid_reconciliation_query") {
		t.Fatalf("oversized details page was accepted: %d %s", invalid.Code, invalid.Body)
	}
}
