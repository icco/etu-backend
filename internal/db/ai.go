package db

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// AIEligibleSubscriptionStatuses lists subscription statuses that may use AI features.
var AIEligibleSubscriptionStatuses = []string{"active", "trialing", "past_due"}

// AIEligible reports whether a user has opted in to AI and has a paid, enabled account.
func AIEligible(u *User) bool {
	if u == nil || !u.AIEnabled || u.Disabled {
		return false
	}
	for _, s := range AIEligibleSubscriptionStatuses {
		if u.SubscriptionStatus == s {
			return true
		}
	}
	return false
}

// ConsumeAIQuota atomically records one AI operation for userID on the UTC day
// of now. It returns false without recording when the user is not currently
// AI eligible or already reached limit.
func (db *DB) ConsumeAIQuota(ctx context.Context, userID string, now time.Time, limit int) (bool, error) {
	if limit <= 0 {
		return false, nil
	}
	u := now.UTC()
	day := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)

	var counts []int
	err := db.conn.WithContext(ctx).Raw(
		`INSERT INTO "AIUsage" ("userId", "day", "count") `+
			`SELECT "id", ?, 1 FROM "User" WHERE "id" = ? AND "aiEnabled" AND NOT "disabled" AND "subscriptionStatus" IN ? `+
			`ON CONFLICT ("userId", "day") DO UPDATE SET "count" = "AIUsage"."count" + 1 `+
			`WHERE "AIUsage"."count" < ? RETURNING "count"`,
		day, userID, AIEligibleSubscriptionStatuses, limit,
	).Scan(&counts).Error
	if err != nil {
		return false, fmt.Errorf("failed to consume AI quota: %w", err)
	}
	return len(counts) > 0, nil
}

// PendingAIImage is an image without extracted text plus the ID of the user who owns it.
type PendingAIImage struct {
	NoteImage `gorm:"embedded"`
	OwnerID   string `gorm:"column:ownerId"`
}

// PendingAIAudio is an audio file without a transcription plus the ID of the user who owns it.
type PendingAIAudio struct {
	NoteAudio `gorm:"embedded"`
	OwnerID   string `gorm:"column:ownerId"`
}

// ListPendingAIImages returns images without extracted text owned by AI eligible users, ordered by owner.
func (db *DB) ListPendingAIImages(ctx context.Context) ([]PendingAIImage, error) {
	var rows []PendingAIImage
	if err := db.pendingAIMedia(ctx, "NoteImage", colExtractedText).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list pending AI images: %w", err)
	}
	return rows, nil
}

// ListPendingAIAudios returns audio files without transcription owned by AI eligible users, ordered by owner.
func (db *DB) ListPendingAIAudios(ctx context.Context) ([]PendingAIAudio, error) {
	var rows []PendingAIAudio
	if err := db.pendingAIMedia(ctx, "NoteAudio", colTranscribedText).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list pending AI audios: %w", err)
	}
	return rows, nil
}

func (db *DB) pendingAIMedia(ctx context.Context, table, textCol string) *gorm.DB {
	return db.conn.WithContext(ctx).
		Table(fmt.Sprintf(`"%s"`, table)).
		Select(fmt.Sprintf(`"%s".*, "Note"."userId" AS "ownerId"`, table)).
		Joins(fmt.Sprintf(`JOIN "Note" ON "Note"."id" = "%s"."noteId"`, table)).
		Joins(`JOIN "User" ON "User"."id" = "Note"."userId"`).
		Where(fmt.Sprintf(`"%s"."%s" = ? AND "User"."aiEnabled" AND NOT "User"."disabled" AND "User"."subscriptionStatus" IN ?`, table, textCol), "", AIEligibleSubscriptionStatuses).
		Order(`"Note"."userId"`)
}
