package db

import (
	"context"
	"fmt"
	"time"
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

// ListAIEligibleUsers returns users that pass AIEligible.
func (db *DB) ListAIEligibleUsers(ctx context.Context) ([]User, error) {
	var users []User
	err := db.conn.WithContext(ctx).
		Where(`"aiEnabled" = ? AND "disabled" = ? AND "subscriptionStatus" IN ?`, true, false, AIEligibleSubscriptionStatuses).
		Find(&users).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query AI eligible users: %w", err)
	}
	return users, nil
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

// GetImagesWithoutExtractedTextForUser returns a user's images that have no extracted text yet.
func (db *DB) GetImagesWithoutExtractedTextForUser(ctx context.Context, userID string) ([]NoteImage, error) {
	var images []NoteImage
	err := db.conn.WithContext(ctx).
		Select(`"NoteImage".*`).
		Joins(`JOIN "Note" ON "Note"."id" = "NoteImage"."noteId"`).
		Where(`"Note"."userId" = ? AND "NoteImage"."extractedText" = ?`, userID, "").
		Find(&images).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get images without extracted text: %w", err)
	}
	return images, nil
}

// GetAudiosWithoutTranscriptionForUser returns a user's audio files that have no transcription yet.
func (db *DB) GetAudiosWithoutTranscriptionForUser(ctx context.Context, userID string) ([]NoteAudio, error) {
	var audios []NoteAudio
	err := db.conn.WithContext(ctx).
		Select(`"NoteAudio".*`).
		Joins(`JOIN "Note" ON "Note"."id" = "NoteAudio"."noteId"`).
		Where(`"Note"."userId" = ? AND "NoteAudio"."transcribedText" = ?`, userID, "").
		Find(&audios).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get audios without transcription: %w", err)
	}
	return audios, nil
}
