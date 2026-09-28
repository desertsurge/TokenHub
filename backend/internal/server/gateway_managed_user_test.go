package server

import (
	"net/http"
	"testing"
	"time"
)

func seedManagedGatewayUser(t *testing.T, store *GormStore, profileID, email string) {
	t.Helper()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_tenant_managed_"+profileID, "tenant.created", "tenant", "tenant_managed", "tenant_managed", 1, map[string]interface{}{
			"externalId": "tenant_managed", "name": "Managed tenant", "status": "active",
		}),
		gatewayIntegrationEvent("evt_member_managed_"+profileID, "tenant_member.added", "tenant_member", "member_"+profileID, "tenant_managed", 1, map[string]interface{}{
			"externalId": "member_" + profileID, "principalExternalId": profileID, "name": "Managed user", "email": email, "status": "active",
		}),
	} {
		response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
		if response.Code != http.StatusOK {
			t.Fatalf("managed gateway seed failed: %d %s", response.Code, response.Body)
		}
	}
}

func TestGatewayMemberCreatesManagedUserBeforeOIDCLogin(t *testing.T) {
	store := NewMemoryStore()
	seedManagedGatewayUser(t, store, "profile_managed", "managed@example.test")

	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "profile_managed").Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Email != "managed@example.test" || user.Status != StatusActive || user.Role != "user" {
		t.Fatalf("unexpected managed user: %+v", user)
	}
	if _, _, err := store.AuthenticateAdminUser(user.Email, "any-password", time.Hour); err == nil {
		t.Fatal("managed user accepted local password login")
	}
	if _, err := store.UpdateAdminUser(user.ID, AdminUser{Name: "Changed locally"}, ""); err == nil {
		t.Fatal("managed user accepted local update")
	}

	server := NewWithConfig(store, Config{SecretKey: "test_secret"})
	provider := managedGatewayProvider()
	claims := map[string]any{"external_user_id": "profile_managed", "organization_id": "tenant_managed", "sub": "pairwise-subject", "email": user.Email}
	loggedIn, err := server.upsertOAuthAdminUser(provider, claims)
	if err != nil || loggedIn.ID != user.ID {
		t.Fatalf("managed OIDC login did not reuse pre-created user: user=%+v err=%v", loggedIn, err)
	}
	if _, err := server.upsertOAuthAdminUser(provider, map[string]any{"external_user_id": "profile_managed", "organization_id": "tenant_managed", "sub": "different-subject"}); err == nil {
		t.Fatal("managed OIDC login accepted a changed subject")
	}
	if _, err := server.upsertOAuthAdminUser(AdminResource{}, map[string]any{"email": user.Email}); err == nil {
		t.Fatal("unmanaged OIDC provider adopted managed user's email")
	}
}

func TestGatewayManagedProviderRequiresExplicitOIDCClaims(t *testing.T) {
	store := NewMemoryStore()
	seedManagedGatewayUser(t, store, "profile_scoped", "scoped@example.test")
	server := NewWithConfig(store, Config{SecretKey: "test_secret"})
	provider := managedGatewayProvider()
	provider.Fields["scopes"] = "profile email"
	claims := map[string]any{"external_user_id": "profile_scoped", "organization_id": "tenant_managed", "sub": "pairwise-subject"}
	if _, err := server.upsertOAuthAdminUser(provider, claims); AsHTTPError(err).Code != "gateway_identity_provider_invalid" {
		t.Fatalf("provider without openid scope was accepted: %v", err)
	}
	provider.Fields["scopes"] = "openid profile email"
	provider.Fields["provider_type"] = "oauth2"
	if _, err := server.upsertOAuthAdminUser(provider, claims); AsHTTPError(err).Code != "gateway_identity_provider_invalid" {
		t.Fatalf("non-OIDC provider was accepted: %v", err)
	}
	provider = managedGatewayProvider()
	provider.Fields["gateway_principal_claim"] = ""
	if _, err := server.upsertOAuthAdminUser(provider, claims); AsHTTPError(err).Code != "gateway_identity_provider_invalid" {
		t.Fatalf("provider without a principal claim was accepted: %v", err)
	}
	provider = managedGatewayProvider()
	provider.Fields["gateway_tenant_claim"] = ""
	if _, err := server.upsertOAuthAdminUser(provider, claims); AsHTTPError(err).Code != "gateway_identity_provider_invalid" {
		t.Fatalf("provider without a tenant claim was accepted: %v", err)
	}
	provider = managedGatewayProvider()
	provider.Fields["userinfo_url"] = "http://external.example.test/userinfo"
	if _, err := server.upsertOAuthAdminUser(provider, claims); AsHTTPError(err).Code != "gateway_identity_provider_invalid" {
		t.Fatalf("provider with insecure userinfo endpoint was accepted: %v", err)
	}
	provider = managedGatewayProvider()
	if _, err := server.upsertOAuthAdminUser(provider, map[string]any{"external_user_id": "profile_scoped", "sub": "pairwise-subject"}); AsHTTPError(err).Code != "gateway_identity_invalid" {
		t.Fatalf("userinfo without tenant claim was accepted: %v", err)
	}
	if _, err := server.upsertOAuthAdminUser(provider, claims); err != nil {
		t.Fatalf("provider with separate secure endpoint hosts was rejected: %v", err)
	}
}

func managedGatewayProvider() AdminResource {
	return AdminResource{ID: "managed-provider", Fields: map[string]any{
		"gateway_managed": true, "provider_type": "oidc", "issuer_url": "https://idp.example.test",
		"authorize_url": "https://login.example.test/authorize", "token_url": "https://api.example.test/oauth/token", "userinfo_url": "https://api.example.test/userinfo",
		"jwks_url": "https://idp.example.test/.well-known/jwks.json",
		"scopes":   "openid profile email", "gateway_principal_claim": "external_user_id", "gateway_tenant_claim": "organization_id",
	}}
}

func TestGatewayTenantDisableInvalidatesManagedSession(t *testing.T) {
	store := NewMemoryStore()
	seedManagedGatewayUser(t, store, "profile_disabled", "disabled@example.test")
	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "profile_disabled").Error; err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(managed.AdminUserID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	event := gatewayIntegrationEvent("evt_tenant_disabled", "tenant.disabled", "tenant", "tenant_managed", "tenant_managed", 2, map[string]interface{}{
		"externalId": "tenant_managed", "name": "Managed tenant", "status": "disabled",
	})
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
	if response.Code != http.StatusOK {
		t.Fatalf("tenant disable failed: %d %s", response.Code, response.Body)
	}
	if _, ok := store.ValidateAdminSession(session.Token); ok {
		t.Fatal("disabled tenant retained a managed admin session")
	}
}

func TestGatewayManagedUserRemainsActiveInAnotherTenant(t *testing.T) {
	store := NewMemoryStore()
	seedManagedGatewayUser(t, store, "profile_shared", "shared@example.test")
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_tenant_second", "tenant.created", "tenant", "tenant_second", "tenant_second", 1, map[string]interface{}{
			"externalId": "tenant_second", "name": "Second tenant", "status": "active",
		}),
		gatewayIntegrationEvent("evt_member_second", "tenant_member.added", "tenant_member", "member_second", "tenant_second", 1, map[string]interface{}{
			"externalId": "member_second", "principalExternalId": "profile_shared", "name": "Shared user", "email": "shared@example.test", "status": "active",
		}),
		gatewayIntegrationEvent("evt_tenant_first_disabled", "tenant.disabled", "tenant", "tenant_managed", "tenant_managed", 2, map[string]interface{}{
			"externalId": "tenant_managed", "name": "Managed tenant", "status": "disabled",
		}),
	} {
		response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
		if response.Code != http.StatusOK {
			t.Fatalf("cross-tenant event failed: %d %s", response.Code, response.Body)
		}
	}
	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "profile_shared").Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Status != StatusActive {
		t.Fatalf("shared user was disabled with one remaining active tenant: %s", user.Status)
	}
	if _, err := store.ResolveGatewayManagedOIDCUser("profile_shared", "tenant_managed", "https://idp.example.test", "shared-subject"); AsHTTPError(err).Code != "gateway_membership_inactive" {
		t.Fatalf("disabled tenant accepted managed login: %v", err)
	}
	if resolved, err := store.ResolveGatewayManagedOIDCUser("profile_shared", "tenant_second", "https://idp.example.test", "shared-subject"); err != nil || resolved.ID != user.ID {
		t.Fatalf("active tenant failed managed login: user=%+v err=%v", resolved, err)
	}
}

func TestGatewayManagedUserRejectsExistingEmail(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{Username: "local", Email: "local@example.test", Role: "user"}, "LocalPassword123!"); err != nil {
		t.Fatal(err)
	}
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	tenant := gatewayIntegrationEvent("evt_tenant_collision", "tenant.created", "tenant", "tenant_collision", "tenant_collision", 1, map[string]interface{}{"externalId": "tenant_collision", "name": "Collision tenant"})
	if response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", tenant, "integration_token"); response.Code != http.StatusOK {
		t.Fatalf("tenant seed failed: %d %s", response.Code, response.Body)
	}
	member := gatewayIntegrationEvent("evt_member_collision", "tenant_member.added", "tenant_member", "member_collision", "tenant_collision", 1, map[string]interface{}{
		"externalId": "member_collision", "principalExternalId": "profile_collision", "name": "Other user", "email": "local@example.test",
	})
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", member, "integration_token")
	if response.Code != http.StatusConflict || !jsonBodyHasCode(response.Body, "gateway_user_conflict") {
		t.Fatalf("expected explicit email conflict, got %d %s", response.Code, response.Body)
	}
	var count int64
	if err := store.db.Model(&GatewayPrincipal{}).Where("external_principal_id = ?", "profile_collision").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("conflicted principal was committed: count=%d err=%v", count, err)
	}
}

func TestGatewayManagedUserIgnoresStaleEmailUpdate(t *testing.T) {
	store := NewMemoryStore()
	seedManagedGatewayUser(t, store, "profile_stale", "previous@example.test")
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()

	current := gatewayIntegrationEvent("evt_member_current", "tenant_member.updated", "tenant_member", "member_profile_stale", "tenant_managed", 2, map[string]interface{}{
		"externalId": "member_profile_stale", "principalExternalId": "profile_stale", "name": "Current user", "email": "current@example.test", "status": "active",
	})
	applyGatewayIntegrationEventForTest(t, app, current)
	if _, err := store.CreateAdminUser(AdminUser{Username: "previous-owner", Email: "previous@example.test", Role: "user"}, "LocalPassword123!"); err != nil {
		t.Fatal(err)
	}

	stale := gatewayIntegrationEvent("evt_member_stale_email", "tenant_member.updated", "tenant_member", "member_profile_stale", "tenant_managed", 1, map[string]interface{}{
		"externalId": "member_profile_stale", "principalExternalId": "profile_stale", "name": "Previous user", "email": "previous@example.test", "status": "active",
	})
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", stale, "integration_token")
	if response.Code != http.StatusOK || !jsonBodyHasField(response.Body, "outcome", "ignored_stale") {
		t.Fatalf("expected stale email event to be ignored, got %d: %s", response.Code, response.Body)
	}

	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "profile_stale").Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Email != "current@example.test" || user.Name != "Current user" {
		t.Fatalf("stale event changed managed user: %+v", user)
	}
}

func TestGatewayManagedUserAcceptsSameVersionInitialEmail(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_tenant_enrichment", "tenant.created", "tenant", "tenant_enrichment", "tenant_enrichment", 1, map[string]interface{}{
		"externalId": "tenant_enrichment", "name": "Enrichment tenant", "status": "active",
	}))
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_member_without_email", "tenant_member.added", "tenant_member", "member_enrichment", "tenant_enrichment", 1, map[string]interface{}{
		"externalId": "member_enrichment", "principalExternalId": "profile_enrichment", "name": "Enriched user", "status": "active",
	}))

	enriched := gatewayIntegrationEvent("evt_member_with_email", "tenant_member.updated", "tenant_member", "member_enrichment", "tenant_enrichment", 1, map[string]interface{}{
		"externalId": "member_enrichment", "principalExternalId": "profile_enrichment", "name": "Enriched user", "email": "enriched@example.test", "status": "active",
	})
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", enriched, "integration_token")
	if response.Code != http.StatusOK || !jsonBodyHasField(response.Body, "outcome", "ignored_stale") {
		t.Fatalf("expected same-version initial email enrichment, got %d: %s", response.Code, response.Body)
	}

	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "profile_enrichment").Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Email != "enriched@example.test" || user.Name != "Enriched user" {
		t.Fatalf("same-version enrichment did not create managed user: %+v", user)
	}
}
