package server

func paginateGatewayProjectionDetails(summary *GatewayIntegrationReconciliationSummary, page, pageSize int) {
	summary.ProjectionDetailsPage = page
	summary.ProjectionDetailsPageSize = pageSize
	summary.ProjectionDetailTotals = make(map[string]int64, len(summary.ProjectionDetails))
	start := (page - 1) * pageSize
	for aggregateType, items := range summary.ProjectionDetails {
		summary.ProjectionDetailTotals[aggregateType] = int64(len(items))
		if start >= len(items) {
			summary.ProjectionDetails[aggregateType] = []gatewayProjectionDigestItem{}
			continue
		}
		end := start + pageSize
		if end > len(items) {
			end = len(items)
		}
		summary.ProjectionDetails[aggregateType] = items[start:end]
	}
}
