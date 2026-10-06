package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestAdminManagedIdentityProviderMutationValidation(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "managed-idp-admin", SecretKey: "managed-idp-secret"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	app := server.Handler()

	invalid := managedGatewayProvider()
	invalid.Name = "Invalid managed identity"
	invalid.Status = StatusActive
	delete(invalid.Fields, "jwks_url")
	created := doJSON(t, app, http.MethodPost, "/api/admin/resources/identity-providers", invalid, "managed-idp-admin")
	if created.Code != http.StatusBadRequest || !strings.Contains(created.Body, "gateway_identity_provider_invalid") {
		t.Fatalf("active managed provider without a key source = %d: %s", created.Code, created.Body)
	}

	missingClientID := managedGatewayProvider()
	missingClientID.Name = "Managed identity without client ID"
	missingClientID.Status = StatusActive
	delete(missingClientID.Fields, "client_id")
	missingClientResponse := doJSON(t, app, http.MethodPost, "/api/admin/resources/identity-providers", missingClientID, "managed-idp-admin")
	if missingClientResponse.Code != http.StatusBadRequest || !strings.Contains(missingClientResponse.Body, "gateway_identity_provider_invalid") {
		t.Fatalf("active managed provider without a client ID = %d: %s", missingClientResponse.Code, missingClientResponse.Body)
	}

	invalid.Status = StatusDisabled
	disabled := doJSON(t, app, http.MethodPost, "/api/admin/resources/identity-providers", invalid, "managed-idp-admin")
	if disabled.Code != http.StatusCreated {
		t.Fatalf("disabled managed provider draft = %d: %s", disabled.Code, disabled.Body)
	}
	disabledID := responseResourceID(t, disabled.Body)

	activated := doJSON(t, app, http.MethodPatch, "/api/admin/resources/identity-providers/"+disabledID, map[string]any{
		"status": StatusActive,
	}, "managed-idp-admin")
	if activated.Code != http.StatusBadRequest || !strings.Contains(activated.Body, "gateway_identity_provider_invalid") {
		t.Fatalf("activation without a key source = %d: %s", activated.Code, activated.Body)
	}

	valid := managedGatewayProvider()
	valid.Name = "Valid managed identity"
	valid.Status = StatusActive
	delete(valid.Fields, "jwks_url")
	valid.Fields["discovery_url"] = "https://idp.example.test/.well-known/openid-configuration"
	accepted := doJSON(t, app, http.MethodPost, "/api/admin/resources/identity-providers", valid, "managed-idp-admin")
	if accepted.Code != http.StatusCreated {
		t.Fatalf("active managed provider with discovery = %d: %s", accepted.Code, accepted.Body)
	}
}
