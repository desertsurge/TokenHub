package server

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newGatewayManagedUserTestStore(t *testing.T) *GormStore {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+NewID("managed_link")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migrateSchemaObjects(database, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := gatewayManagedUserMigration().Go(t.Context(), directSQLMigrationExecer{DB: sqlDB}); err != nil {
		t.Fatal(err)
	}
	if err := gatewayExistingUserLinkMigration().Go(t.Context(), directSQLMigrationExecer{DB: sqlDB}); err != nil {
		t.Fatal(err)
	}
	return &GormStore{db: database, dbDriver: "sqlite", mu: &sync.Mutex{}}
}

func createGatewayManagedUserTestPrincipal(t *testing.T, store *GormStore, principalID string) GatewayPrincipal {
	t.Helper()
	tenant := GatewayTenant{ID: "tenant-" + principalID, ExternalTenantID: "tenant-" + principalID, Status: StatusActive}
	principal := GatewayPrincipal{ID: "principal-" + principalID, TenantID: tenant.ID, ExternalPrincipalID: principalID, Status: StatusActive}
	if err := store.db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	return principal
}

func TestGatewayManagedUserLinksExistingAdminOnCurrentSchema(t *testing.T) {
	store := newGatewayManagedUserTestStore(t)
	database := store.db
	admin, err := store.CreateAdminUser(AdminUser{Username: "existing-admin", Email: "admin@example.test", Role: "admin", Status: StatusActive}, "LocalPassword123!")
	if err != nil {
		t.Fatal(err)
	}
	tenant := GatewayTenant{ID: "tenant-local", ExternalTenantID: "tenant-local", Status: StatusActive}
	principal := GatewayPrincipal{ID: "principal-local", TenantID: tenant.ID, ExternalPrincipalID: "aip-admin", DisplayName: "AIP name", Status: StatusActive}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	event := GatewayIntegrationEvent{OccurredAt: time.Now().UTC(), Payload: map[string]interface{}{"email": "ADMIN@example.test"}}
	if err := database.Transaction(func(tx *gorm.DB) error { return store.syncGatewayManagedUser(tx, principal, event) }); err != nil {
		t.Fatal(err)
	}
	var managed GatewayManagedUser
	if err := database.First(&managed, "external_principal_id = ?", principal.ExternalPrincipalID).Error; err != nil || managed.AdminUserID != admin.ID || !managed.LinkedExisting {
		t.Fatalf("existing admin link = %+v, err = %v", managed, err)
	}
	if _, _, err := store.AuthenticateAdminUser(admin.Email, "LocalPassword123!", time.Hour); err != nil {
		t.Fatalf("local login failed after link: %v", err)
	}
	server := NewWithConfig(store, Config{SecretKey: "test_secret"})
	if _, err := server.upsertOAuthAdminUser(AdminResource{}, map[string]any{"email": admin.Email}); AsHTTPError(err).Code != "gateway_identity_conflict" {
		t.Fatalf("unmanaged OAuth adopted linked admin: %v", err)
	}
	if _, err := store.UpdateAdminUser(admin.ID, AdminUser{Username: admin.Username, Name: "Locally managed", Email: admin.Email, Role: "admin", Status: StatusActive}, ""); err != nil {
		t.Fatalf("linked admin became read-only: %v", err)
	}
	if err := store.DeleteAdminUser(admin.ID); AsHTTPError(err).Code != "gateway_managed_user_read_only" {
		t.Fatalf("linked admin deletion was allowed: %v", err)
	}
	resolved, err := store.ResolveGatewayManagedOIDCUser(principal.ExternalPrincipalID, tenant.ExternalTenantID, "https://idp.example.test", "admin-subject")
	if err != nil || resolved.ID != admin.ID || resolved.Role != "admin" {
		t.Fatalf("managed OIDC did not preserve admin role: user=%+v err=%v", resolved, err)
	}
	other := GatewayPrincipal{ID: "principal-other", TenantID: tenant.ID, ExternalPrincipalID: "aip-other", Status: StatusActive}
	if err := database.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Transaction(func(tx *gorm.DB) error { return store.syncGatewayManagedUser(tx, other, event) }); AsHTTPError(err).Code != "gateway_user_conflict" {
		t.Fatalf("another principal claimed the admin account: %v", err)
	}
	changedEmail := GatewayIntegrationEvent{OccurredAt: event.OccurredAt.Add(time.Minute), Payload: map[string]interface{}{"email": "changed@example.test"}}
	if err := database.Transaction(func(tx *gorm.DB) error { return store.syncGatewayManagedUser(tx, principal, changedEmail) }); err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := database.First(&user, "id = ?", admin.ID).Error; err != nil || user.Email != admin.Email || user.Name != "Locally managed" {
		t.Fatalf("principal email update overwrote linked admin: user=%+v err=%v", user, err)
	}
	principal.Status = StatusDisabled
	if err := database.Save(&principal).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Transaction(func(tx *gorm.DB) error { return refreshGatewayManagedPrincipalState(tx, principal) }); err != nil {
		t.Fatal(err)
	}
	if err := database.First(&user, "id = ?", admin.ID).Error; err != nil || user.Status != StatusActive || user.Role != "admin" {
		t.Fatalf("principal status overwrote linked admin: user=%+v err=%v", user, err)
	}
}

func TestGatewayManagedUserLinksEmailDespiteGeneratedUsernameCollision(t *testing.T) {
	store := newGatewayManagedUserTestStore(t)
	principalID := "aip-email-match"
	if _, err := store.CreateAdminUser(AdminUser{Username: gatewayManagedUsername(principalID), Email: "other@example.test", Role: "user"}, "LocalPassword123!"); err != nil {
		t.Fatal(err)
	}
	admin, err := store.CreateAdminUser(AdminUser{Username: "existing-admin", Email: "admin@example.test", Role: "admin"}, "LocalPassword123!")
	if err != nil {
		t.Fatal(err)
	}
	principal := createGatewayManagedUserTestPrincipal(t, store, principalID)
	event := GatewayIntegrationEvent{OccurredAt: time.Now().UTC(), Payload: map[string]interface{}{"email": admin.Email}}
	if err := store.db.Transaction(func(tx *gorm.DB) error { return store.syncGatewayManagedUser(tx, principal, event) }); err != nil {
		t.Fatal(err)
	}
	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", principalID).Error; err != nil || managed.AdminUserID != admin.ID || !managed.LinkedExisting {
		t.Fatalf("email match was not linked: managed=%+v err=%v", managed, err)
	}
}

func TestGatewayManagedUserRejectsAmbiguousEmail(t *testing.T) {
	store := newGatewayManagedUserTestStore(t)
	for _, user := range []AdminUser{
		{Username: "first-admin", Email: "admin@example.test", Role: "admin"},
		{Username: "second-admin", Email: "ADMIN@example.test", Role: "admin"},
	} {
		if _, err := store.CreateAdminUser(user, "LocalPassword123!"); err != nil {
			t.Fatal(err)
		}
	}
	principal := createGatewayManagedUserTestPrincipal(t, store, "aip-ambiguous")
	event := GatewayIntegrationEvent{OccurredAt: time.Now().UTC(), Payload: map[string]interface{}{"email": "admin@example.test"}}
	if err := store.db.Transaction(func(tx *gorm.DB) error { return store.syncGatewayManagedUser(tx, principal, event) }); AsHTTPError(err).Code != "gateway_user_conflict" {
		t.Fatalf("ambiguous email claimed an account: %v", err)
	}
}

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
		"client_id": "managed-client",
		"jwks_url":  "https://idp.example.test/.well-known/jwks.json",
		"scopes":    "openid profile email", "gateway_principal_claim": "external_user_id", "gateway_tenant_claim": "organization_id",
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

func TestGatewayManagedUserLinksExistingEmailWithoutTakingOverAccount(t *testing.T) {
	store := NewMemoryStore()
	existing, err := store.CreateAdminUser(AdminUser{Username: "local", Name: "Local admin", Email: "local@example.test", Role: "admin", Status: StatusActive}, "LocalPassword123!")
	if err != nil {
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
	for _, event := range []map[string]interface{}{member, member} {
		response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
		if response.Code != http.StatusOK {
			t.Fatalf("existing account link failed: %d %s", response.Code, response.Body)
		}
	}
	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "profile_collision").Error; err != nil || managed.AdminUserID != existing.ID || !managed.LinkedExisting {
		t.Fatalf("existing account was not linked: %+v err=%v", managed, err)
	}
	var user AdminUser
	if err := store.db.First(&user, "id = ?", existing.ID).Error; err != nil || user.Role != "admin" || user.Name != "Local admin" || user.Status != StatusActive {
		t.Fatalf("existing account changed: %+v err=%v", user, err)
	}
	if _, _, err := store.AuthenticateAdminUser(existing.Email, "LocalPassword123!", time.Hour); err != nil {
		t.Fatalf("existing local login was blocked: %v", err)
	}
	if _, err := store.UpdateAdminUser(existing.ID, AdminUser{Username: existing.Username, Name: "Updated locally", Email: existing.Email, Role: existing.Role, Status: existing.Status}, ""); err != nil {
		t.Fatalf("existing account became read-only: %v", err)
	}
	server := NewWithConfig(store, Config{SecretKey: "test_secret"})
	provider := managedGatewayProvider()
	resolved, err := server.upsertOAuthAdminUser(provider, map[string]any{"external_user_id": "profile_collision", "organization_id": "tenant_collision", "sub": "local-subject"})
	if err != nil || resolved.ID != existing.ID || resolved.Role != "admin" {
		t.Fatalf("managed OIDC did not use linked admin: user=%+v err=%v", resolved, err)
	}
	second := gatewayIntegrationEvent("evt_member_other", "tenant_member.added", "tenant_member", "member_other", "tenant_collision", 1, map[string]interface{}{
		"externalId": "member_other", "principalExternalId": "profile_other", "name": "Other user", "email": "local@example.test",
	})
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", second, "integration_token")
	if response.Code != http.StatusConflict || !jsonBodyHasCode(response.Body, "gateway_user_conflict") {
		t.Fatalf("another principal adopted linked admin: %d %s", response.Code, response.Body)
	}
	var count int64
	if err := store.db.Model(&GatewayPrincipal{}).Where("external_principal_id = ?", "profile_other").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("conflicted principal was committed: count=%d err=%v", count, err)
	}
	updated := gatewayIntegrationEvent("evt_member_disabled", "tenant_member.updated", "tenant_member", "member_collision", "tenant_collision", 2, map[string]interface{}{
		"externalId": "member_collision", "principalExternalId": "profile_collision", "name": "Renamed by AIP", "email": "new@example.test", "status": "disabled",
	})
	response = doJSON(t, app, http.MethodPost, "/api/internal/integration/events", updated, "integration_token")
	if response.Code != http.StatusOK {
		t.Fatalf("linked principal update failed: %d %s", response.Code, response.Body)
	}
	if err := store.db.First(&user, "id = ?", existing.ID).Error; err != nil || user.Status != StatusActive || user.Email != existing.Email || user.Role != "admin" {
		t.Fatalf("principal update changed existing account: %+v err=%v", user, err)
	}
	if _, _, err := store.AuthenticateAdminUser(existing.Email, "LocalPassword123!", time.Hour); err != nil {
		t.Fatalf("principal disable blocked existing local login: %v", err)
	}
	if err := store.DeleteAdminUser(existing.ID); AsHTTPError(err).Code != "gateway_managed_user_read_only" {
		t.Fatalf("linked account deletion was allowed: %v", err)
	}
	if _, err := server.upsertOAuthAdminUser(provider, map[string]any{"external_user_id": "profile_collision", "organization_id": "tenant_collision", "sub": "local-subject"}); AsHTTPError(err).Code != "gateway_membership_inactive" {
		t.Fatalf("disabled principal accepted managed OIDC: %v", err)
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

func TestGatewayManagedUserIgnoresOlderProfileFromAnotherTenant(t *testing.T) {
	store := NewMemoryStore()
	seedManagedGatewayUser(t, store, "profile_cross_tenant_stale", "initial@example.test")
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()

	current := gatewayIntegrationEvent("evt_member_cross_tenant_current", "tenant_member.updated", "tenant_member", "member_profile_cross_tenant_stale", "tenant_managed", 2, map[string]interface{}{
		"externalId": "member_profile_cross_tenant_stale", "principalExternalId": "profile_cross_tenant_stale", "name": "Current user", "email": "current@example.test", "status": "active",
	})
	current["occurredAt"] = "2026-07-25T08:00:00.000Z"
	applyGatewayIntegrationEventForTest(t, app, current)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_tenant_cross_tenant_stale", "tenant.created", "tenant", "tenant_cross_tenant_stale", "tenant_cross_tenant_stale", 1, map[string]interface{}{
		"externalId": "tenant_cross_tenant_stale", "name": "Second tenant", "status": "active",
	}))
	older := gatewayIntegrationEvent("evt_member_cross_tenant_older", "tenant_member.added", "tenant_member", "member_cross_tenant_older", "tenant_cross_tenant_stale", 1, map[string]interface{}{
		"externalId": "member_cross_tenant_older", "principalExternalId": "profile_cross_tenant_stale", "name": "Older user", "email": "older@example.test", "status": "active",
	})
	older["occurredAt"] = "2026-07-24T08:00:00.000Z"
	applyGatewayIntegrationEventForTest(t, app, older)

	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "profile_cross_tenant_stale").Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Email != "current@example.test" || user.Name != "Current user" {
		t.Fatalf("older cross-tenant event changed managed user: %+v", user)
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
	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "profile_enrichment").Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Email != gatewayManagedSyntheticEmail("profile_enrichment") {
		t.Fatalf("email-free projection did not create the expected synthetic user: %+v", user)
	}

	enriched := gatewayIntegrationEvent("evt_member_with_email", "tenant_member.updated", "tenant_member", "member_enrichment", "tenant_enrichment", 1, map[string]interface{}{
		"externalId": "member_enrichment", "principalExternalId": "profile_enrichment", "name": "Enriched user", "email": "enriched@example.test", "status": "active",
	})
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", enriched, "integration_token")
	if response.Code != http.StatusOK || !jsonBodyHasField(response.Body, "outcome", "ignored_stale") {
		t.Fatalf("expected same-version initial email enrichment, got %d: %s", response.Code, response.Body)
	}

	user = AdminUser{}
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Email != "enriched@example.test" || user.Name != "Enriched user" {
		t.Fatalf("same-version enrichment did not create managed user: %+v", user)
	}
}

func TestGatewayManagedUserIgnoresOlderSameVersionEmail(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_tenant_same_version", "tenant.created", "tenant", "tenant_same_version", "tenant_same_version", 1, map[string]interface{}{
		"externalId": "tenant_same_version", "name": "Same-version tenant", "status": "active",
	}))
	current := gatewayIntegrationEvent("evt_member_same_version_current", "tenant_member.added", "tenant_member", "member_same_version", "tenant_same_version", 1, map[string]interface{}{
		"externalId": "member_same_version", "principalExternalId": "profile_same_version", "name": "Current user", "email": "current@example.test", "status": "active",
	})
	current["occurredAt"] = "2026-07-25T08:00:00.000Z"
	applyGatewayIntegrationEventForTest(t, app, current)

	older := gatewayIntegrationEvent("evt_member_same_version_older", "tenant_member.updated", "tenant_member", "member_same_version", "tenant_same_version", 1, map[string]interface{}{
		"externalId": "member_same_version", "principalExternalId": "profile_same_version", "name": "Older user", "email": "older@example.test", "status": "active",
	})
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", older, "integration_token")
	if response.Code != http.StatusOK || !jsonBodyHasField(response.Body, "outcome", "ignored_stale") {
		t.Fatalf("expected older same-version event to be ignored, got %d: %s", response.Code, response.Body)
	}

	var principal GatewayPrincipal
	if err := store.db.First(&principal, "external_principal_id = ?", "profile_same_version").Error; err != nil {
		t.Fatal(err)
	}
	if principal.Email != "current@example.test" || principal.DisplayName != "Current user" {
		t.Fatalf("older same-version event changed tenant principal: %+v", principal)
	}
	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "profile_same_version").Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Email != "current@example.test" || user.Name != "Current user" {
		t.Fatalf("older same-version event changed managed user: %+v", user)
	}
}

func TestGatewayManagedUserKeepsLatestSameVersionEmailTimestamp(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_tenant_same_version_order", "tenant.created", "tenant", "tenant_same_version_order", "tenant_same_version_order", 1, map[string]interface{}{
		"externalId": "tenant_same_version_order", "name": "Same-version ordering tenant", "status": "active",
	}))

	newer := gatewayIntegrationEvent("evt_member_same_version_newer", "tenant_member.added", "tenant_member", "member_same_version_order", "tenant_same_version_order", 1, map[string]interface{}{
		"externalId": "member_same_version_order", "principalExternalId": "profile_same_version_order", "name": "Newest user", "email": "newest@example.test", "status": "active",
	})
	newer["occurredAt"] = "2026-07-25T10:00:00.000Z"
	applyGatewayIntegrationEventForTest(t, app, newer)

	middle := gatewayIntegrationEvent("evt_member_same_version_middle", "tenant_member.updated", "tenant_member", "member_same_version_order", "tenant_same_version_order", 1, map[string]interface{}{
		"externalId": "member_same_version_order", "principalExternalId": "profile_same_version_order", "name": "Middle user", "email": "middle@example.test", "status": "active",
	})
	middle["occurredAt"] = "2026-07-25T09:00:00.000Z"
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", middle, "integration_token")
	if response.Code != http.StatusOK || !jsonBodyHasField(response.Body, "outcome", "ignored_stale") {
		t.Fatalf("expected middle same-version event to be ignored, got %d: %s", response.Code, response.Body)
	}

	var principal GatewayPrincipal
	if err := store.db.First(&principal, "external_principal_id = ?", "profile_same_version_order").Error; err != nil {
		t.Fatal(err)
	}
	if principal.Email != "newest@example.test" || principal.DisplayName != "Newest user" {
		t.Fatalf("middle same-version event changed the latest principal: %+v", principal)
	}
	var managed GatewayManagedUser
	if err := store.db.First(&managed, "external_principal_id = ?", "profile_same_version_order").Error; err != nil {
		t.Fatal(err)
	}
	var user AdminUser
	if err := store.db.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		t.Fatal(err)
	}
	if user.Email != "newest@example.test" || user.Name != "Newest user" {
		t.Fatalf("middle same-version event changed the latest managed user: %+v", user)
	}
}

func TestGatewayManagedUserAdvancesTimestampWithoutEmail(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_tenant_same_version_no_email", "tenant.created", "tenant", "tenant_same_version_no_email", "tenant_same_version_no_email", 1, map[string]interface{}{
		"externalId": "tenant_same_version_no_email", "name": "Same-version no-email tenant", "status": "active",
	}))
	current := gatewayIntegrationEvent("evt_member_same_version_email_current", "tenant_member.added", "tenant_member", "member_same_version_no_email", "tenant_same_version_no_email", 1, map[string]interface{}{
		"externalId": "member_same_version_no_email", "principalExternalId": "profile_same_version_no_email", "name": "Current user", "email": "current@example.test", "status": "active",
	})
	current["occurredAt"] = "2026-07-25T10:00:00.000Z"
	applyGatewayIntegrationEventForTest(t, app, current)

	newerWithoutEmail := gatewayIntegrationEvent("evt_member_same_version_no_email_newer", "tenant_member.updated", "tenant_member", "member_same_version_no_email", "tenant_same_version_no_email", 1, map[string]interface{}{
		"externalId": "member_same_version_no_email", "principalExternalId": "profile_same_version_no_email", "name": "Current user", "status": "active",
	})
	newerWithoutEmail["occurredAt"] = "2026-07-25T11:00:00.000Z"
	applyGatewayIntegrationEventForTest(t, app, newerWithoutEmail)

	olderEmail := gatewayIntegrationEvent("evt_member_same_version_email_older", "tenant_member.updated", "tenant_member", "member_same_version_no_email", "tenant_same_version_no_email", 1, map[string]interface{}{
		"externalId": "member_same_version_no_email", "principalExternalId": "profile_same_version_no_email", "name": "Older user", "email": "older@example.test", "status": "active",
	})
	olderEmail["occurredAt"] = "2026-07-25T10:30:00.000Z"
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", olderEmail, "integration_token")
	if response.Code != http.StatusOK || !jsonBodyHasField(response.Body, "outcome", "ignored_stale") {
		t.Fatalf("expected older email event to be ignored after timestamp-only update, got %d: %s", response.Code, response.Body)
	}

	var principal GatewayPrincipal
	if err := store.db.First(&principal, "external_principal_id = ?", "profile_same_version_no_email").Error; err != nil {
		t.Fatal(err)
	}
	if principal.Email != "current@example.test" || !principal.SourceOccurredAt.Equal(time.Date(2026, 7, 25, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("older email event rewrote the principal after timestamp-only update: %+v", principal)
	}
}
