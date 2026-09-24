package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"tokenhub/backend/internal/dbschema"
)

type GatewayManagedUser struct {
	ExternalPrincipalID string `gorm:"primaryKey"`
	AdminUserID         string
	OIDCIssuer          string `gorm:"column:oidc_issuer"`
	OIDCSubject         string `gorm:"column:oidc_subject"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func gatewayManagedUserMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          9,
		Name:             "add-gateway-managed-users",
		Go:               addGatewayManagedUsers,
		ChecksumOverride: "tokenhub-schema-gateway-managed-users-v1",
		StatementBudget:  4,
	}
}

func addGatewayManagedUsers(ctx context.Context, db dbschema.MigrationExecer) error {
	dateType := "datetime"
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err == nil {
		dateType = "timestamptz"
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS gateway_managed_users (
			external_principal_id text PRIMARY KEY,
			admin_user_id text NOT NULL,
			oidc_issuer text NOT NULL DEFAULT '',
			oidc_subject text NOT NULL DEFAULT '',
			created_at ` + dateType + ` NOT NULL,
			updated_at ` + dateType + ` NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_managed_user_admin ON gateway_managed_users (admin_user_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_managed_user_oidc ON gateway_managed_users (oidc_issuer, oidc_subject) WHERE oidc_subject <> ''`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func gatewayManagedUsername(principalID string) string {
	digest := sha256.Sum256([]byte(principalID))
	return "gateway_" + hex.EncodeToString(digest[:16])
}

func syncGatewayManagedUser(tx *gorm.DB, principal GatewayPrincipal, event GatewayIntegrationEvent) error {
	principalID := principal.ExternalPrincipalID
	email := strings.TrimSpace(payloadString(event.Payload, "email"))
	var managed GatewayManagedUser
	err := tx.First(&managed, "external_principal_id = ?", principalID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if email == "" {
			return nil
		}
		var conflict int64
		if err := tx.Model(&AdminUser{}).Where("lower(email) = lower(?) OR username = ?", email, gatewayManagedUsername(principalID)).Count(&conflict).Error; err != nil {
			return err
		}
		if conflict > 0 {
			return NewHTTPError(http.StatusConflict, "gateway_user_conflict", "Gateway user email or username belongs to another account")
		}
		user, err := createAdminUser(tx, AdminUser{
			Username: gatewayManagedUsername(principalID),
			Name:     principal.DisplayName,
			Email:    email,
			Role:     "user",
			Status:   StatusActive,
		}, GenerateAdminSessionToken())
		if err != nil {
			return err
		}
		managed = GatewayManagedUser{ExternalPrincipalID: principalID, AdminUserID: user.ID}
		if err := tx.Create(&managed).Error; err != nil {
			return err
		}
	}

	var user AdminUser
	if err := tx.First(&user, "id = ?", managed.AdminUserID).Error; err != nil {
		return NewHTTPError(http.StatusConflict, "gateway_user_missing", "Managed gateway user is missing")
	}
	if email != "" && !strings.EqualFold(user.Email, email) {
		var conflict int64
		if err := tx.Model(&AdminUser{}).Where("id <> ? AND lower(email) = lower(?)", user.ID, email).Count(&conflict).Error; err != nil {
			return err
		}
		if conflict > 0 {
			return NewHTTPError(http.StatusConflict, "gateway_user_conflict", "Gateway user email belongs to another account")
		}
		user.Email = email
	}
	if principal.DisplayName != "" {
		user.Name = principal.DisplayName
	}
	active, err := gatewayManagedUserActive(tx, principalID)
	if err != nil {
		return err
	}
	user.Status = StatusDisabled
	if active {
		user.Status = StatusActive
	}
	user.UpdatedAt = time.Now().UTC()
	if err := tx.Save(&user).Error; err != nil {
		return err
	}
	var projects []GatewayProject
	if err := tx.Where("owner_principal_id = ?", principal.ID).Find(&projects).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, project := range projects {
		if err := syncGatewayServingProject(tx, project, now); err != nil {
			return err
		}
	}
	return refreshGatewayManagedUserTeams(tx, principalID)
}

func gatewayManagedUserActive(tx *gorm.DB, principalID string) (bool, error) {
	var activeCount int64
	err := tx.Model(&GatewayPrincipal{}).
		Joins("JOIN gateway_tenants ON gateway_tenants.id = gateway_principals.tenant_id").
		Where("gateway_principals.external_principal_id = ? AND gateway_principals.status = ? AND gateway_principals.deleted_at IS NULL AND gateway_tenants.status = ? AND gateway_tenants.deleted_at IS NULL", principalID, StatusActive, StatusActive).
		Count(&activeCount).Error
	return activeCount > 0, err
}

func refreshGatewayManagedTenantUsers(tx *gorm.DB, tenantID string) error {
	var principals []GatewayPrincipal
	if err := tx.Where("tenant_id = ?", tenantID).Find(&principals).Error; err != nil {
		return err
	}
	for _, principal := range principals {
		var managed GatewayManagedUser
		if err := tx.First(&managed, "external_principal_id = ?", principal.ExternalPrincipalID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		} else if err != nil {
			return err
		}
		active, err := gatewayManagedUserActive(tx, principal.ExternalPrincipalID)
		if err != nil {
			return err
		}
		status := StatusDisabled
		if active {
			status = StatusActive
		}
		if err := tx.Model(&AdminUser{}).Where("id = ?", managed.AdminUserID).
			Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		if err := refreshGatewayManagedUserTeams(tx, principal.ExternalPrincipalID); err != nil {
			return err
		}
	}
	return nil
}

func (s *GormStore) IsGatewayManagedAdminUser(userID string) bool {
	var count int64
	err := s.db.Model(&GatewayManagedUser{}).Where("admin_user_id = ?", userID).Count(&count).Error
	return err != nil || count > 0
}

func (s *GormStore) ResolveGatewayManagedOIDCUser(principalID, tenantExternalID, issuer, subject string) (AdminUser, error) {
	if principalID == "" || tenantExternalID == "" || issuer == "" || subject == "" {
		return AdminUser{}, NewHTTPError(http.StatusForbidden, "gateway_identity_invalid", "Managed OIDC identity is incomplete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var user AdminUser
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var managed GatewayManagedUser
		if err := tx.First(&managed, "external_principal_id = ?", principalID).Error; err != nil {
			return NewHTTPError(http.StatusForbidden, "gateway_user_not_synced", "Gateway user has not been synchronized")
		}
		var principal GatewayPrincipal
		if err := tx.Joins("JOIN gateway_tenants ON gateway_tenants.id = gateway_principals.tenant_id").
			First(&principal, "gateway_principals.external_principal_id = ? AND gateway_tenants.external_tenant_id = ? AND gateway_principals.status = ? AND gateway_principals.deleted_at IS NULL AND gateway_tenants.status = ? AND gateway_tenants.deleted_at IS NULL", principalID, tenantExternalID, StatusActive, StatusActive).Error; err != nil {
			return NewHTTPError(http.StatusForbidden, "gateway_membership_inactive", "Gateway tenant membership is not active")
		}
		if managed.OIDCSubject != "" && (managed.OIDCIssuer != issuer || managed.OIDCSubject != subject) {
			return NewHTTPError(http.StatusConflict, "gateway_identity_conflict", "OIDC identity does not match the synchronized user")
		}
		var existing GatewayManagedUser
		if err := tx.First(&existing, "oidc_issuer = ? AND oidc_subject = ?", issuer, subject).Error; err == nil && existing.ExternalPrincipalID != principalID {
			return NewHTTPError(http.StatusConflict, "gateway_identity_conflict", "OIDC identity is already bound to another user")
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if managed.OIDCSubject == "" {
			managed.OIDCIssuer, managed.OIDCSubject = issuer, subject
			if err := tx.Save(&managed).Error; err != nil {
				return err
			}
		}
		if err := tx.First(&user, "id = ? AND status = ?", managed.AdminUserID, StatusActive).Error; err != nil {
			return NewHTTPError(http.StatusForbidden, "gateway_user_inactive", "Managed gateway user is not active")
		}
		return nil
	})
	return publicAdminUser(user), err
}
