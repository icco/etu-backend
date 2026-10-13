package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"go.icco.me/etu-backend/internal/db"
	"go.icco.me/gutil/logging"
	"go.uber.org/zap"
)

const defaultDailyAILimit = 200

func dailyAILimit() (int, error) {
	v := os.Getenv("AI_DAILY_LIMIT")
	if v == "" {
		return defaultDailyAILimit, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("AI_DAILY_LIMIT must be a non-negative integer, got %q", v)
	}
	return n, nil
}

// allowAI fails closed so a quota store outage cannot cause unbounded spend.
func allowAI(ctx context.Context, database *db.DB, userID string, limit int) bool {
	ok, err := database.ConsumeAIQuota(ctx, userID, time.Now(), limit)
	if err != nil {
		logging.FromContext(ctx).Errorw("failed to consume AI quota", "user_id", userID, zap.Error(err))
		return false
	}
	if !ok {
		logging.FromContext(ctx).Infow("daily AI limit reached", "user_id", userID, "limit", limit)
	}
	return ok
}
