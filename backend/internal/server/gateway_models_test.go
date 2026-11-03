package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGatewayModelsOnlyReturnsConfiguredExternalDirectory(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{ID: "catalog_only", Name: "catalog-only", Category: "chat", Metadata: map[string]string{"source": "tokenhub-standard-catalog"}, Status: StatusActive})
	store.AddModel(Model{ID: "provider_catalog_only", Name: "provider-catalog-only", Category: "chat", Metadata: map[string]string{"source": "public-provider-conf"}, Status: StatusActive})
	store.AddModel(Model{ID: "configured_draft", Name: "configured-draft", Category: "chat", Metadata: map[string]string{"source": "tokenhub-standard-catalog", modelDirectoryRoleKey: modelDirectoryRoleExternal}, Status: StatusActive})
	store.AddModel(Model{ID: "configured_routed", Name: "configured-routed", Category: "chat", Metadata: map[string]string{"source": "tokenhub-standard-catalog"}, Status: StatusDisabled})
	store.AddModel(Model{ID: "legacy_custom", Name: "legacy-custom", Category: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_configured", ModelName: "configured-routed", Status: StatusDisabled})
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()

	type modelPage struct {
		Data  []gatewayModelItem `json:"data"`
		Total int                `json:"total"`
	}
	readPage := func(path string) modelPage {
		t.Helper()
		response := doJSON(t, app, http.MethodGet, path, nil, "integration_token")
		if response.Code != http.StatusOK {
			t.Fatalf("expected configured model list, got %d: %s", response.Code, response.Body)
		}
		var page modelPage
		if err := json.Unmarshal([]byte(response.Body), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}

	all := readPage("/api/internal/models?category=chat&page_size=10")
	if all.Total != 3 || len(all.Data) != 3 ||
		all.Data[0].Name != "configured-draft" || all.Data[1].Name != "configured-routed" || all.Data[2].Name != "legacy-custom" ||
		all.Data[1].Status != StatusDisabled {
		t.Fatalf("expected configured draft, routed and legacy models without catalog seeds: %+v", all)
	}
	second := readPage("/api/internal/models?category=chat&page=2&page_size=1")
	if second.Total != 3 || len(second.Data) != 1 || second.Data[0].Name != "configured-routed" {
		t.Fatalf("catalog seeds must not affect configured model pagination: %+v", second)
	}
}
