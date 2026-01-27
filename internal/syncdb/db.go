package syncdb

import (
	"fmt"
	"os"
	"time"

	"github.com/icco/etu-backend/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB wraps the GORM database connection
type DB struct {
	conn *gorm.DB
}

// Re-export models for backwards compatibility
type Note = models.Note
type Tag = models.Tag
type NoteTag = models.NoteTag
type User = models.User
type SyncState = models.SyncState

// New creates a new GORM database connection
func New() (*DB, error) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable not set")
	}

	conn, err := gorm.Open(postgres.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure connection pool
	sqlDB, err := conn.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	return &DB{conn: conn}, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	sqlDB, err := db.conn.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// AutoMigrate runs auto migrations for all tables
func (db *DB) AutoMigrate() error {
	return db.conn.AutoMigrate(
		&models.User{},
		&models.Note{},
		&models.Tag{},
		&models.NoteTag{},
		&models.ApiKey{},
		&models.SyncState{},
	)
}

// GetNoteByNotionPageID finds a note by its Notion page ID (externalId)
func (db *DB) GetNoteByNotionPageID(userID, pageID string) (*Note, error) {
	var note Note
	result := db.conn.Where(`"userId" = ? AND "externalId" = ?`, userID, pageID).First(&note)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return &note, nil
}

// GetNoteByNotionUUID finds a note by its Notion UUID (stored in ID property)
func (db *DB) GetNoteByNotionUUID(userID, notionUUID string) (*Note, error) {
	var note Note
	result := db.conn.Where(`"userId" = ? AND "notionUuid" = ?`, userID, notionUUID).First(&note)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return &note, nil
}

// UpsertNoteFromNotion creates or updates a note from Notion data
func (db *DB) UpsertNoteFromNotion(userID, notionUUID, pageID, content string, tagNames []string, createdAt, updatedAt time.Time) (*Note, bool, error) {
	var note Note
	var isNew bool

	err := db.conn.Transaction(func(tx *gorm.DB) error {
		// Try to find existing note by Notion UUID first, then by page ID
		result := tx.Where(`"userId" = ? AND "notionUuid" = ?`, userID, notionUUID).First(&note)
		if result.Error == gorm.ErrRecordNotFound {
			// Try by page ID (for backwards compatibility)
			result = tx.Where(`"userId" = ? AND "externalId" = ?`, userID, pageID).First(&note)
		}

		if result.Error == gorm.ErrRecordNotFound {
			// Create new note
			isNew = true
			note = Note{
				ID:         models.GenerateCUID(),
				Content:    content,
				CreatedAt:  createdAt,
				UpdatedAt:  updatedAt,
				UserID:     userID,
				ExternalID: &pageID,
				NotionUUID: &notionUUID,
			}
			if err := tx.Create(&note).Error; err != nil {
				return fmt.Errorf("failed to create note: %w", err)
			}
		} else if result.Error != nil {
			return result.Error
		} else {
			// Update existing note
			isNew = false
			note.Content = content
			note.UpdatedAt = updatedAt
			note.ExternalID = &pageID
			note.NotionUUID = &notionUUID
			if err := tx.Save(&note).Error; err != nil {
				return fmt.Errorf("failed to update note: %w", err)
			}
		}

		// Clear existing tag associations
		if err := tx.Where(`"noteId" = ?`, note.ID).Delete(&NoteTag{}).Error; err != nil {
			return fmt.Errorf("failed to clear tag associations: %w", err)
		}

		// Create/find tags and associate them
		for _, tagName := range tagNames {
			if tagName == "" {
				continue
			}

			var tag Tag
			result := tx.Where(`"userId" = ? AND name = ?`, userID, tagName).First(&tag)
			if result.Error == gorm.ErrRecordNotFound {
				// Create new tag
				tag = Tag{
					ID:        models.GenerateCUID(),
					Name:      tagName,
					CreatedAt: time.Now(),
					UserID:    userID,
				}
				if err := tx.Create(&tag).Error; err != nil {
					return fmt.Errorf("failed to create tag: %w", err)
				}
			} else if result.Error != nil {
				return result.Error
			}

			// Create association
			noteTag := NoteTag{NoteID: note.ID, TagID: tag.ID}
			if err := tx.Create(&noteTag).Error; err != nil {
				// Ignore duplicate key errors
				if err.Error() != "ERROR: duplicate key value violates unique constraint" {
					return fmt.Errorf("failed to associate tag: %w", err)
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, false, err
	}

	return &note, isNew, nil
}

// GetLastSyncTime returns the last sync time for a user
func (db *DB) GetLastSyncTime(userID string) (*time.Time, error) {
	var state SyncState
	result := db.conn.Where(`"userId" = ?`, userID).First(&state)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return &state.LastSyncedAt, nil
}

// UpdateLastSyncTime updates the last sync time for a user
func (db *DB) UpdateLastSyncTime(userID string, syncTime time.Time) error {
	state := SyncState{
		UserID:       userID,
		LastSyncedAt: syncTime,
	}
	return db.conn.Save(&state).Error
}

// GetNoteTags returns the tag names for a note
func (db *DB) GetNoteTags(noteID string) ([]string, error) {
	var tags []Tag
	err := db.conn.
		Joins(`JOIN "NoteTag" ON "NoteTag"."tagId" = "Tag".id`).
		Where(`"NoteTag"."noteId" = ?`, noteID).
		Find(&tags).Error
	if err != nil {
		return nil, err
	}

	names := make([]string, len(tags))
	for i, tag := range tags {
		names[i] = tag.Name
	}
	return names, nil
}
