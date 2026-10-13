package db

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ExportCursor is the keyset position of the last note returned by ExportNotes.
type ExportCursor struct {
	CreatedAt time.Time
	ID        string
}

// DeleteUserData permanently deletes a user and every row they own in a single transaction.
func (db *DB) DeleteUserData(ctx context.Context, userID string) error {
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
	var names []string
	err := db.conn.WithContext(ctx).Raw(
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
