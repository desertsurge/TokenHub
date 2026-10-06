package server

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

// hydrateAsyncJobAttribution fills missing durable ownership fields from the
// API key while the job is created. Admitted jobs already carry an ownership
// snapshot; preserving it keeps quota settlement aligned with admission even
// if the backing identity mapping changes before persistence.
func hydrateAsyncJobAttribution(tx *gorm.DB, projectID, apiKeyID string, tenantExternalID *string, attributedUserID *string) error {
	if tx == nil || strings.TrimSpace(apiKeyID) == "" {
		return nil
	}
	var key APIKey
	if err := tx.First(&key, "id = ?", strings.TrimSpace(apiKeyID)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if tenantExternalID != nil {
		if value := strings.TrimSpace(*tenantExternalID); value != "" {
			*tenantExternalID = value
		} else {
			*tenantExternalID = strings.TrimSpace(key.TenantExternalID)
		}
	}
	if attributedUserID == nil {
		return nil
	}
	project := Project{ID: strings.TrimSpace(projectID)}
	if project.ID != "" {
		if err := tx.First(&project, "id = ?", project.ID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if value := strings.TrimSpace(*attributedUserID); value != "" {
		*attributedUserID = value
	} else {
		*attributedUserID = usageAttributionUserIDWithDB(tx, key, project)
	}
	return nil
}

func resolvedUsageAttributionUserID(store Store, key APIKey, project Project) string {
	return store.ResolveUsageAttributionUserID(key, project)
}

func (s *GormStore) ResolveUsageAttributionUserID(key APIKey, project Project) string {
	if s == nil || s.db == nil {
		return usageAttributionUserID(key, project)
	}
	return usageAttributionUserIDWithDB(s.db, key, project)
}
