package model

// The agent dashboard queries copy GetQuotaDataByUserId and
// getSelfFlowQuotaData with the single user swapped for the agent's scoped
// user ids. Keep them in step with those functions when upstream changes them.

// GetAgentQuotaData is GetQuotaDataByUserId over every user in userIDs. Rows
// stay split per user, so the dashboard can both break usage down by user and
// add it up.
func GetAgentQuotaData(userIDs []int, startTime int64, endTime int64) ([]*QuotaData, error) {
	var quotaDatas []*QuotaData
	err := DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").
		Where("user_id IN ? and created_at >= ? and created_at <= ?", userIDs, startTime, endTime).
		Group("user_id, username, model_name, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

// GetAgentFlowQuotaData is getSelfFlowQuotaData over every user in userIDs,
// split per user. Like the self view it resolves token names only; channel
// names stay hidden from agents.
func GetAgentFlowQuotaData(userIDs []int, startTime int64, endTime int64) ([]*FlowQuotaData, error) {
	rows := make([]*FlowQuotaData, 0)
	err := flowQuotaBaseQuery(startTime, endTime).
		Select("user_id, username, token_id, use_group, model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("user_id IN ?", userIDs).
		Group("user_id, username, token_id, use_group, model_name").
		Order("quota DESC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, fillFlowTokenNames(rows)
}
