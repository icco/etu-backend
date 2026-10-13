package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.icco.me/etu-backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ExportCursor is the keyset position of the last note returned by ExportNotes.
type ExportCursor struct {
	CreatedAt time.Time
	ID        string
}

// DeleteUserData permanently deletes a user and every row they own in a single transaction.
// It locks the user row first, so in-flight note writes either commit before it (and are
// deleted here) or fail afterwards. It records media not in alreadyDeleted, plus the user's profile prefix, as
// AccountDeletionCleanup rows in the same transaction so a failed media sweep can be retried.
func (db *DB) DeleteUserData(ctx context.Context, userID string, alreadyDeleted []string) error {
	const notesOf = `(SELECT id FROM "Note" WHERE "userId" = ?)`
	statements := []string{
		`DELETE FROM "NoteTag" WHERE "noteId" IN ` + notesOf,
		`DELETE FROM "NoteImage" WHERE "noteId" IN ` + notesOf,
		`DELETE FROM "NoteAudio" WHERE "noteId" IN ` + notesOf,
		`DELETE FROM "NoteTag" WHERE "tagId" IN (SELECT id FROM "Tag" WHERE "userId" = ?)`,
		`DELETE FROM "Note" WHERE "userId" = ?`,
		`DELETE FROM "Tag" WHERE "userId" = ?`,
		`DELETE FROM "ApiKey" WHERE "userId" = ?`,
		`DELETE FROM "SyncState" WHERE "userId" = ?`,
		`DELETE FROM "User" WHERE id = ?`,
	}
	return db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked []string
		if err := tx.Raw(`SELECT id FROM "User" WHERE id = ? FOR UPDATE`, userID).Scan(&locked).Error; err != nil {
			return fmt.Errorf("lock user: %w", err)
		}
		objects, err := userMediaObjects(tx, userID)
		if err != nil {
			return err
		}
		if err := recordCleanup(tx, userID, objects, alreadyDeleted); err != nil {
			return err
		}
		for _, stmt := range statements {
			if err := tx.Exec(stmt, userID).Error; err != nil {
				return fmt.Errorf("delete user data: %w", err)
			}
		}
		return nil
	})
}

// UserMediaObjects returns the GCS object names of every attachment on the user's notes.
func (db *DB) UserMediaObjects(ctx context.Context, userID string) ([]string, error) {
	return userMediaObjects(db.conn.WithContext(ctx), userID)
}

func userMediaObjects(tx *gorm.DB, userID string) ([]string, error) {
	var names []string
	err := tx.Raw(
		`SELECT "NoteImage"."gcsObjectName" FROM "NoteImage" JOIN "Note" ON "Note".id = "NoteImage"."noteId" WHERE "Note"."userId" = ?`+
			` UNION SELECT "NoteAudio"."gcsObjectName" FROM "NoteAudio" JOIN "Note" ON "Note".id = "NoteAudio"."noteId" WHERE "Note"."userId" = ?`,
		userID, userID,
	).Scan(&names).Error
	if err != nil {
		return nil, fmt.Errorf("list user media objects: %w", err)
	}
	out := names[:0]
	for _, n := range names {
		if n != "" {
			out = append(out, n)
		}
	}
	return out, nil
}

// ExportNotes returns up to limit of the user's notes after cursor in (createdAt, id) order,
// with tags, images, and audio attached. A nil cursor starts from the beginning.
func (db *DB) ExportNotes(ctx context.Context, userID string, cursor *ExportCursor, limit int) ([]Note, error) {
	query := db.conn.WithContext(ctx).Where(`"userId" = ?`, userID)
	if cursor != nil {
		query = query.Where(`("createdAt", id) > (?, ?)`, cursor.CreatedAt, cursor.ID)
	}
	var notes []Note
	if err := query.Order(`"createdAt" ASC, id ASC`).Limit(limit).Find(&notes).Error; err != nil {
		return nil, fmt.Errorf("export notes: %w", err)
	}
	if len(notes) == 0 {
		return notes, nil
	}

	noteIDs := make([]string, len(notes))
	for i, n := range notes {
		noteIDs[i] = n.ID
	}
	tags, err := db.getTagsForNotes(ctx, noteIDs)
	if err != nil {
		return nil, fmt.Errorf("export note tags: %w", err)
	}
	images, err := db.getImagesForNotes(ctx, noteIDs)
	if err != nil {
		return nil, fmt.Errorf("export note images: %w", err)
	}
	var audios []NoteAudio
	if err := db.conn.WithContext(ctx).Where(`"noteId" IN ?`, noteIDs).Order(`"createdAt" ASC`).Find(&audios).Error; err != nil {
		return nil, fmt.Errorf("export note audios: %w", err)
	}
	audiosByNote := make(map[string][]NoteAudio)
	for _, a := range audios {
		audiosByNote[a.NoteID] = append(audiosByNote[a.NoteID], a)
	}
	for i := range notes {
		notes[i].Tags = tags[notes[i].ID]
		notes[i].Images = images[notes[i].ID]
		notes[i].Audios = audiosByNote[notes[i].ID]
	}
	return notes, nil
}

// DeletionDisabledReason marks accounts frozen by an in-progress or failed account deletion.
const DeletionDisabledReason = "account deletion in progress"

// FreezeUserForDeletion disables the user and revokes all of their API keys in one
// committed transaction so no new data can be written while deletion runs.
func (db *DB) FreezeUserForDeletion(ctx context.Context, userID string) error {
	return db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE "User" SET disabled = true, "disabledReason" = ? WHERE id = ?`, DeletionDisabledReason, userID).Error; err != nil {
			return fmt.Errorf("disable user: %w", err)
		}
		if err := tx.Exec(`DELETE FROM "ApiKey" WHERE "userId" = ?`, userID).Error; err != nil {
			return fmt.Errorf("revoke api keys: %w", err)
		}
		return nil
	})
}

// ErrUserDisabled is returned by note writes for a missing or disabled user.
var ErrUserDisabled = errors.New("user is disabled")

// lockActiveUser share-locks the user row so writes commit before, or fail after, account deletion.
func lockActiveUser(tx *gorm.DB, userID string) error {
	var ids []string
	if err := tx.Raw(`SELECT id FROM "User" WHERE id = ? AND disabled IS NOT TRUE FOR SHARE`, userID).Scan(&ids).Error; err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	if len(ids) == 0 {
		return ErrUserDisabled
	}
	return nil
}

// lockActiveNoteOwner share-locks the owner of noteID; see lockActiveUser.
func lockActiveNoteOwner(tx *gorm.DB, noteID string) error {
	var ids []string
	err := tx.Raw(`SELECT "User".id FROM "User" JOIN "Note" ON "Note"."userId" = "User".id WHERE "Note".id = ? AND "User".disabled IS NOT TRUE FOR SHARE OF "User"`, noteID).Scan(&ids).Error
	if err != nil {
		return fmt.Errorf("lock note owner: %w", err)
	}
	if len(ids) == 0 {
		return ErrUserDisabled
	}
	return nil
}

func recordCleanup(tx *gorm.DB, userID string, objects, alreadyDeleted []string) error {
	done := make(map[string]bool, len(alreadyDeleted))
	for _, name := range alreadyDeleted {
		done[name] = true
	}
	now := time.Now()
	var rows []models.AccountDeletionCleanup
	for _, name := range append(objects, "profiles/"+userID+"/") {
		if !done[name] {
			rows = append(rows, models.AccountDeletionCleanup{UserID: userID, ObjectName: name, CreatedAt: now})
		}
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 500).Error; err != nil {
		return fmt.Errorf("record deletion cleanup: %w", err)
	}
	return nil
}

// ListDeletionCleanup returns the GCS objects and prefixes still to remove for a deleted user.
func (db *DB) ListDeletionCleanup(ctx context.Context, userID string) ([]string, error) {
	var names []string
	if err := db.conn.WithContext(ctx).Raw(`SELECT "objectName" FROM "AccountDeletionCleanup" WHERE "userId" = ? ORDER BY "objectName"`, userID).Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("list deletion cleanup: %w", err)
	}
	return names, nil
}

// RemoveDeletionCleanup marks one cleanup object as done.
func (db *DB) RemoveDeletionCleanup(ctx context.Context, userID, objectName string) error {
	err := db.conn.WithContext(ctx).Where(`"userId" = ? AND "objectName" = ?`, userID, objectName).Delete(&models.AccountDeletionCleanup{}).Error
	if err != nil {
		return fmt.Errorf("remove deletion cleanup: %w", err)
	}
	return nil
}
