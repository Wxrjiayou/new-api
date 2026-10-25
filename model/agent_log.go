package model

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// The agent log queries copy GetUserLogs, StreamUserLogs, and SumUsedQuota
// with the single user swapped for the agent's scoped user ids. Keep their
// filters in step with those functions when upstream changes them.

// formatAgentLogs shapes logs like a user's own log list and also drops the
// channel id, which an agent must not see.
func formatAgentLogs(logs []*Log, startIdx int) {
	formatUserLogs(logs, startIdx)
	for _, log := range logs {
		log.ChannelId = 0
	}
}

// GetAgentLogs is GetUserLogs over every user in userIDs.
func GetAgentLogs(userIDs []int, filter LogFilter, startIdx int, num int) (logs []*Log, total int64, err error) {
	tx, err := filter.apply(LOG_DB.Where("logs.user_id IN ?", userIDs))
	if err != nil {
		return nil, 0, err
	}
	err = tx.Model(&Log{}).Limit(logSearchCountLimit).Count(&total).Error
	if err != nil {
		common.SysError(common.LogText("failed to count agent logs: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to query logs")
	}
	order := "logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		common.SysError(common.LogText("failed to search agent logs: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to query logs")
	}

	formatAgentLogs(logs, startIdx)
	return logs, total, err
}

// StreamAgentLogs is StreamUserLogs over every user in userIDs.
func StreamAgentLogs(ctx context.Context, userIDs []int, filter LogFilter, fn func(*Log) error) error {
	tx, err := filter.apply(LOG_DB.WithContext(ctx).Where("logs.user_id IN ?", userIDs))
	if err != nil {
		return err
	}
	count := 0
	return streamLogs(tx, "logs.id desc", func(log *Log) error {
		count++
		log.Id = count
		log.ChannelId = 0
		log.ChannelName = ""
		log.Other = formatLogOtherJSON(log.Other, logOtherVisibilityUser)
		return fn(log)
	})
}

// SumAgentUsedQuota is SumUsedQuota over every user in userIDs. It matches
// users by id, never by username, because usernames can change.
func SumAgentUsedQuota(userIDs []int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, group string) (stat Stat, err error) {
	tx := LOG_DB.Table("logs").Select("COALESCE(sum(quota), 0) quota").Where("user_id IN ?", userIDs)
	rpmTpmQuery := LOG_DB.Table("logs").Select("count(*) rpm, COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0) tpm").Where("user_id IN ?", userIDs)

	if tx, err = applyExplicitLogTextFilter(tx, "username", username); err != nil {
		return stat, err
	}
	if rpmTpmQuery, err = applyExplicitLogTextFilter(rpmTpmQuery, "username", username); err != nil {
		return stat, err
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
		rpmTpmQuery = rpmTpmQuery.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if tx, err = applyExplicitLogTextFilter(tx, "model_name", modelName); err != nil {
		return stat, err
	}
	if rpmTpmQuery, err = applyExplicitLogTextFilter(rpmTpmQuery, "model_name", modelName); err != nil {
		return stat, err
	}
	if group != "" {
		tx = tx.Where(logGroupCol+" = ?", group)
		rpmTpmQuery = rpmTpmQuery.Where(logGroupCol+" = ?", group)
	}

	tx = tx.Where("type = ?", LogTypeConsume)
	rpmTpmQuery = rpmTpmQuery.Where("type = ?", LogTypeConsume)
	rpmTpmQuery = rpmTpmQuery.Where("created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	if err := tx.Scan(&stat).Error; err != nil {
		common.SysError(common.LogText("failed to query log stat: %s", err.Error()))
		return stat, common.NewMessage("Failed to query statistics")
	}
	var rateStat struct {
		Rpm int
		Tpm int
	}
	if err := rpmTpmQuery.Scan(&rateStat).Error; err != nil {
		common.SysError(common.LogText("failed to query rpm/tpm stat: %s", err.Error()))
		return stat, common.NewMessage("Failed to query statistics")
	}
	stat.Rpm = rateStat.Rpm
	stat.Tpm = rateStat.Tpm
	return stat, nil
}
