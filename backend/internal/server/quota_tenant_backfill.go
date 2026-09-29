package server

import "gorm.io/gorm"

// backfillGatewayModelAccessKeyOwnership binds historical integration keys to
// their managed user. New keys carry OwnerUserID at creation time, while old
// keys may only have the principal metadata needed to resolve the same owner.
func backfillGatewayModelAccessKeyOwnership(db *gorm.DB) error {
	if !db.Migrator().HasTable(&APIKey{}) || !db.Migrator().HasTable(&GatewayManagedUser{}) {
		return nil
	}
	return db.Exec(`
UPDATE api_keys
SET owner_user_id = (
    SELECT admin_user_id FROM gateway_managed_users
    WHERE gateway_managed_users.external_principal_id = api_keys.principal_external_id
)
WHERE managed_by = ? AND principal_type = 'user'
  AND (owner_user_id IS NULL OR owner_user_id = '')
  AND EXISTS (
    SELECT 1 FROM gateway_managed_users
    WHERE gateway_managed_users.external_principal_id = api_keys.principal_external_id
  )`, gatewayModelAccessKeyManagedBy).Error
}

// backfillAsyncJobTenantAttribution repairs durable jobs created before the
// tenant column was introduced. Missing API keys are retained under a reserved
// unassigned marker rather than being guessed into a tenant.
func backfillAsyncJobTenantAttribution(db *gorm.DB) error {
	if !db.Migrator().HasTable(&APIKey{}) {
		return nil
	}
	hasManagedUsers := db.Migrator().HasTable(&GatewayManagedUser{})
	for _, table := range []string{"image_jobs", "response_jobs"} {
		if !db.Migrator().HasTable(table) {
			continue
		}
		if err := db.Exec(`UPDATE `+table+` SET tenant_external_id = (
    SELECT tenant_external_id FROM api_keys WHERE api_keys.id = `+table+`.api_key_id
)
WHERE (tenant_external_id IS NULL OR tenant_external_id = '' OR tenant_external_id = ?)
  AND EXISTS (SELECT 1 FROM api_keys WHERE api_keys.id = `+table+`.api_key_id AND api_keys.tenant_external_id <> '')`, unattributedTenantExternalID).Error; err != nil {
			return err
		}
		if err := db.Exec(`UPDATE `+table+` SET tenant_external_id = ?
WHERE (tenant_external_id IS NULL OR tenant_external_id = '')
  AND NOT EXISTS (SELECT 1 FROM api_keys WHERE api_keys.id = `+table+`.api_key_id)`, unattributedTenantExternalID).Error; err != nil {
			return err
		}
		// A managed user key may have been created before OwnerUserID was
		// introduced. Keep the job attribution aligned with the repaired key.
		if !hasManagedUsers {
			continue
		}
		if err := db.Exec(`UPDATE `+table+` SET attributed_user_id = (
    SELECT COALESCE(NULLIF(api_keys.owner_user_id, ''), gateway_managed_users.admin_user_id)
    FROM api_keys JOIN gateway_managed_users
      ON gateway_managed_users.external_principal_id = api_keys.principal_external_id
    WHERE api_keys.id = `+table+`.api_key_id
      AND api_keys.managed_by = ? AND api_keys.principal_type = 'user'
)
WHERE EXISTS (
    SELECT 1 FROM api_keys JOIN gateway_managed_users
      ON gateway_managed_users.external_principal_id = api_keys.principal_external_id
    WHERE api_keys.id = `+table+`.api_key_id
      AND api_keys.managed_by = ? AND api_keys.principal_type = 'user'
  )`, gatewayModelAccessKeyManagedBy, gatewayModelAccessKeyManagedBy).Error; err != nil {
			return err
		}
	}
	return nil
}
