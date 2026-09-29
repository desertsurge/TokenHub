package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"gorm.io/gorm"
)

func userQuotaBucketKey(userID string, tenantExternalIDs ...string) string {
	userID = strings.TrimSpace(userID)
	if len(tenantExternalIDs) == 0 || strings.TrimSpace(tenantExternalIDs[0]) == "" {
		return "user:" + userID
	}
	// Tenant-scoped user buckets are deliberately opaque so external IDs that
	// contain separators cannot collide. The user attribution column remains
	// readable for reporting and historical migrations.
	scope := strings.TrimSpace(tenantExternalIDs[0]) + "\x00" + userID
	digest := sha256.Sum256([]byte(scope))
	return "user:" + hex.EncodeToString(digest[:])
}

func (s *GormStore) GetQuotaPolicyUsage(scope string, scopeID string, tenantExternalIDs ...string) (QuotaPolicyUsage, bool, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	scopeID = strings.TrimSpace(scopeID)
	tenantExternalID := ""
	if len(tenantExternalIDs) > 0 {
		tenantExternalID = strings.TrimSpace(tenantExternalIDs[0])
	}
	bucketID := ""
	switch scope {
	case "user":
		bucketID = userQuotaBucketKey(scopeID, tenantExternalID)
	case "api_key", "key":
		bucketID = scopeID
	default:
		return QuotaPolicyUsage{}, false, nil
	}
	if scopeID == "" {
		return QuotaPolicyUsage{}, false, nil
	}
	now, err := s.databaseNow(s.db)
	if err != nil {
		return QuotaPolicyUsage{}, false, err
	}
	usage := QuotaPolicyUsage{}
	lookupAttribution := unattributedQuotaUserID
	if scope == "user" {
		lookupAttribution = scopeID
	}
	for _, period := range []struct {
		scope   string
		bucket  string
		counter *QuotaCounter
	}{
		{scope: "minute", bucket: minuteBucket(now), counter: &usage.Minute},
		{scope: "day", bucket: dayBucket(now), counter: &usage.Daily},
		{scope: "month", bucket: monthBucket(now), counter: &usage.Monthly},
	} {
		var item QuotaBucket
		attributionQuery := "attributed_user_id = ?"
		attributionArgs := []any{lookupAttribution}
		if scope == "user" {
			attributionQuery = "attributed_user_id IN (?, '')"
		}
		queryArgs := append([]any{bucketID, period.scope, period.bucket}, attributionArgs...)
		err := s.db.Where("key_id = ? AND scope = ? AND bucket = ? AND "+attributionQuery, queryArgs...).First(&item).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return QuotaPolicyUsage{}, false, err
		}
		if err == nil {
			*period.counter = item.QuotaCounter
		}
		if scope == "user" {
			aggregated, err := s.aggregateUserQuotaCounter(s.db, scopeID, period.scope, period.bucket, tenantExternalID)
			if err != nil {
				return QuotaPolicyUsage{}, false, err
			}
			mergeQuotaCounterMax(period.counter, aggregated)
		}
		if period.scope == "minute" && s.billingRedis != nil {
			redisScopeID := bucketID
			if scope == "user" {
				redisScopeID = userQuotaBucketKey(scopeID, tenantExternalID)
			}
			redisCounter, err := s.billingRedis.minuteCounter(context.Background(), redisScopeID, period.bucket)
			if err != nil {
				return QuotaPolicyUsage{}, false, err
			}
			mergeQuotaCounterMax(period.counter, redisCounter)
		}
	}
	return usage, true, nil
}

func (s *GormStore) aggregateUserQuotaCounter(tx *gorm.DB, userID string, scope string, bucket string, tenantExternalIDs ...string) (QuotaCounter, error) {
	tenantExternalID := ""
	if len(tenantExternalIDs) > 0 {
		tenantExternalID = strings.TrimSpace(tenantExternalIDs[0])
	}
	var aggregate QuotaCounter
	query := tx.Table("quota_buckets AS qb").
		Select("COALESCE(SUM(qb.requests), 0) AS requests, COALESCE(SUM(qb.prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(qb.completion_tokens), 0) AS completion_tokens, COALESCE(SUM(qb.total_tokens), 0) AS total_tokens, COALESCE(SUM(qb.cost_usd), 0) AS cost_usd").
		Where("qb.scope = ? AND qb.bucket = ?", scope, bucket).
		Where("qb.key_id <> ?", userQuotaBucketKey(userID, tenantExternalID)).
		Where("qb.attributed_user_id = ?", strings.TrimSpace(userID))
	if tenantExternalID != "" {
		query = query.Where("qb.tenant_external_id = ?", tenantExternalID)
	}
	err := query.Scan(&aggregate).Error
	return aggregate, err
}

func mergeQuotaCounterMax(target *QuotaCounter, source QuotaCounter) {
	if source.Requests > target.Requests {
		target.Requests = source.Requests
	}
	if source.PromptTokens > target.PromptTokens {
		target.PromptTokens = source.PromptTokens
	}
	if source.CompletionTokens > target.CompletionTokens {
		target.CompletionTokens = source.CompletionTokens
	}
	if source.TotalTokens > target.TotalTokens {
		target.TotalTokens = source.TotalTokens
	}
	if source.CostUSD > target.CostUSD {
		target.CostUSD = source.CostUSD
	}
}
