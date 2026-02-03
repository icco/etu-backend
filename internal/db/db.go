package db

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/icco/etu-backend/internal/models"
	"golang.org/x/crypto/bcrypt"
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
type User = models.User
type ApiKey = models.ApiKey
type NoteImage = models.NoteImage
type NoteAudio = models.NoteAudio

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
		&models.NoteImage{},
		&models.NoteAudio{},
	)
}

// ListNotes retrieves notes for a user with optional filtering
func (db *DB) ListNotes(ctx context.Context, userID, search string, tags []string, startDate, endDate string, limit, offset int) ([]Note, int, error) {
	var notes []Note
	var total int64

	query := db.conn.WithContext(ctx).Model(&Note{}).Where(`"userId" = ?`, userID)

	// Parse tag: syntax from search string
	searchTags, remainingSearch := parseTagSearch(search)
	allTags := append(tags, searchTags...)

	// Tag filtering
	if len(allTags) > 0 {
		query = query.Joins(`JOIN "NoteTag" ON "Note".id = "NoteTag"."noteId"`).
			Joins(`JOIN "Tag" ON "NoteTag"."tagId" = "Tag".id`).
			Where(`"Tag".name IN ?`, allTags).
			Distinct()
	}

	// Search filter (remaining text after tag: extraction)
	if remainingSearch != "" {
		query = query.Where("content ILIKE ?", "%"+remainingSearch+"%")
	}

	// Date filters
	if startDate != "" {
		query = query.Where(`"createdAt" >= ?`, startDate)
	}
	if endDate != "" {
		query = query.Where(`"createdAt" <= ?`, endDate)
	}

	// Get total count
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count notes: %w", err)
	}

	// Get paginated results
	if err := query.Order(`"createdAt" DESC`).Limit(limit).Offset(offset).Find(&notes).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to query notes: %w", err)
	}

	if len(notes) == 0 {
		return notes, int(total), nil
	}

	// Collect note IDs for batch fetching
	noteIDs := make([]string, len(notes))
	for i, n := range notes {
		noteIDs[i] = n.ID
	}

	// Batch fetch tags for all notes
	tagsByNoteID, err := db.getTagsForNotes(ctx, noteIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to batch fetch tags: %w", err)
	}

	// Batch fetch images for all notes
	imagesByNoteID, err := db.getImagesForNotes(ctx, noteIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to batch fetch images: %w", err)
	}

	// Assign tags and images to notes
	for i := range notes {
		notes[i].Tags = tagsByNoteID[notes[i].ID]
		notes[i].Images = imagesByNoteID[notes[i].ID]
	}

	return notes, int(total), nil
}

// getNoteTags retrieves tags for a note
func (db *DB) getNoteTags(ctx context.Context, noteID string) ([]Tag, error) {
	var tags []Tag
	err := db.conn.WithContext(ctx).
		Joins(`JOIN "NoteTag" ON "Tag".id = "NoteTag"."tagId"`).
		Where(`"NoteTag"."noteId" = ?`, noteID).
		Order(`"Tag".name`).
		Find(&tags).Error
	return tags, err
}

// getNoteImages retrieves images for a note
func (db *DB) getNoteImages(ctx context.Context, noteID string) ([]NoteImage, error) {
	var images []NoteImage
	err := db.conn.WithContext(ctx).
		Where(`"noteId" = ?`, noteID).
		Order(`"createdAt" ASC`).
		Find(&images).Error
	return images, err
}

// noteTagResult is used for batch fetching tags with their note associations
type noteTagResult struct {
	NoteID string
	Tag
}

// getTagsForNotes batch fetches tags for multiple notes
func (db *DB) getTagsForNotes(ctx context.Context, noteIDs []string) (map[string][]Tag, error) {
	var results []noteTagResult

	err := db.conn.WithContext(ctx).
		Table(`"Tag"`).
		Select(`"NoteTag"."noteId" as note_id, "Tag".*`).
		Joins(`JOIN "NoteTag" ON "Tag".id = "NoteTag"."tagId"`).
		Where(`"NoteTag"."noteId" IN ?`, noteIDs).
		Order(`"Tag".name`).
		Scan(&results).Error

	if err != nil {
		return nil, err
	}

	// Group tags by note ID
	tagsByNoteID := make(map[string][]Tag)
	for _, noteID := range noteIDs {
		tagsByNoteID[noteID] = []Tag{} // Initialize empty slice for notes with no tags
	}
	for _, r := range results {
		tagsByNoteID[r.NoteID] = append(tagsByNoteID[r.NoteID], r.Tag)
	}

	return tagsByNoteID, nil
}

// getImagesForNotes batch fetches images for multiple notes
func (db *DB) getImagesForNotes(ctx context.Context, noteIDs []string) (map[string][]NoteImage, error) {
	var images []NoteImage

	err := db.conn.WithContext(ctx).
		Where(`"noteId" IN ?`, noteIDs).
		Order(`"createdAt" ASC`).
		Find(&images).Error

	if err != nil {
		return nil, err
	}

	// Group images by note ID
	imagesByNoteID := make(map[string][]NoteImage)
	for _, noteID := range noteIDs {
		imagesByNoteID[noteID] = []NoteImage{} // Initialize empty slice for notes with no images
	}
	for _, img := range images {
		imagesByNoteID[img.NoteID] = append(imagesByNoteID[img.NoteID], img)
	}

	return imagesByNoteID, nil
}

// GetNote retrieves a single note by ID for a user
func (db *DB) GetNote(ctx context.Context, userID, noteID string) (*Note, error) {
	var note Note
	result := db.conn.WithContext(ctx).Where(`id = ? AND "userId" = ?`, noteID, userID).First(&note)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get note: %w", result.Error)
	}

	tags, err := db.getNoteTags(ctx, note.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tags for note: %w", err)
	}
	note.Tags = tags

	images, err := db.getNoteImages(ctx, note.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get images for note: %w", err)
	}
	note.Images = images

	return &note, nil
}

// CreateNote creates a new note with optional tags
func (db *DB) CreateNote(ctx context.Context, userID, content string, tagNames []string) (*Note, error) {
	var note Note

	err := db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		note = Note{
			ID:        models.GenerateCUID(),
			Content:   content,
			CreatedAt: now,
			UpdatedAt: now,
			UserID:    userID,
		}

		if err := tx.Create(&note).Error; err != nil {
			return fmt.Errorf("failed to insert note: %w", err)
		}

		// Create tags and link them
		for _, tagName := range tagNames {
			if tagName == "" {
				continue
			}

			var tag models.Tag
			result := tx.Where(`"userId" = ? AND name = ?`, userID, tagName).First(&tag)
			if result.Error == gorm.ErrRecordNotFound {
				tag = models.Tag{
					ID:        models.GenerateCUID(),
					Name:      tagName,
					CreatedAt: now,
					UserID:    userID,
				}
				if err := tx.Create(&tag).Error; err != nil {
					return fmt.Errorf("failed to create tag: %w", err)
				}
			} else if result.Error != nil {
				return result.Error
			}

			// Link note to tag
			noteTag := models.NoteTag{NoteID: note.ID, TagID: tag.ID}
			if err := tx.Create(&noteTag).Error; err != nil {
				return fmt.Errorf("failed to link note to tag: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Reload tags and images
	tags, err := db.getNoteTags(ctx, note.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tags for note: %w", err)
	}
	note.Tags = tags

	images, err := db.getNoteImages(ctx, note.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get images for note: %w", err)
	}
	note.Images = images

	return &note, nil
}

// UpdateNote updates an existing note
func (db *DB) UpdateNote(ctx context.Context, userID, noteID string, content *string, tagNames []string, updateTags bool) (*Note, error) {
	var note Note

	err := db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Verify ownership and get current note
		result := tx.Where(`id = ? AND "userId" = ?`, noteID, userID).First(&note)
		if result.Error == gorm.ErrRecordNotFound {
			return nil
		}
		if result.Error != nil {
			return fmt.Errorf("failed to verify note ownership: %w", result.Error)
		}

		now := time.Now()
		if content != nil {
			note.Content = *content
		}
		note.UpdatedAt = now

		if err := tx.Save(&note).Error; err != nil {
			return fmt.Errorf("failed to update note: %w", err)
		}

		// Update tags if requested
		if updateTags {
			// Remove existing tag links
			if err := tx.Where(`"noteId" = ?`, noteID).Delete(&models.NoteTag{}).Error; err != nil {
				return fmt.Errorf("failed to remove tag links: %w", err)
			}

			// Add new tags
			for _, tagName := range tagNames {
				if tagName == "" {
					continue
				}

				var tag models.Tag
				result := tx.Where(`"userId" = ? AND name = ?`, userID, tagName).First(&tag)
				if result.Error == gorm.ErrRecordNotFound {
					tag = models.Tag{
						ID:        models.GenerateCUID(),
						Name:      tagName,
						CreatedAt: now,
						UserID:    userID,
					}
					if err := tx.Create(&tag).Error; err != nil {
						return fmt.Errorf("failed to create tag: %w", err)
					}
				} else if result.Error != nil {
					return result.Error
				}

				noteTag := models.NoteTag{NoteID: noteID, TagID: tag.ID}
				if err := tx.Create(&noteTag).Error; err != nil {
					return fmt.Errorf("failed to link note to tag: %w", err)
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if note.ID == "" {
		return nil, nil
	}

	// Reload tags and images
	tags, err := db.getNoteTags(ctx, note.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tags for note: %w", err)
	}
	note.Tags = tags

	images, err := db.getNoteImages(ctx, note.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get images for note: %w", err)
	}
	note.Images = images

	return &note, nil
}

// DeleteNote deletes a note by ID for a user
func (db *DB) DeleteNote(ctx context.Context, userID, noteID string) (bool, error) {
	result := db.conn.WithContext(ctx).Where(`id = ? AND "userId" = ?`, noteID, userID).Delete(&Note{})
	if result.Error != nil {
		return false, fmt.Errorf("failed to delete note: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// AddImageToNote adds an image to a note
func (db *DB) AddImageToNote(ctx context.Context, noteID string, image *NoteImage) error {
	image.NoteID = noteID
	if image.CreatedAt.IsZero() {
		image.CreatedAt = time.Now()
	}
	if err := db.conn.WithContext(ctx).Create(image).Error; err != nil {
		return fmt.Errorf("failed to add image to note: %w", err)
	}
	return nil
}

// RemoveImageFromNote removes an image from a note and returns the GCS object name for cleanup
func (db *DB) RemoveImageFromNote(ctx context.Context, userID, noteID, imageID string) (string, error) {
	// First verify the note belongs to the user
	var note Note
	result := db.conn.WithContext(ctx).Where(`id = ? AND "userId" = ?`, noteID, userID).First(&note)
	if result.Error == gorm.ErrRecordNotFound {
		return "", fmt.Errorf("note not found")
	}
	if result.Error != nil {
		return "", fmt.Errorf("failed to verify note ownership: %w", result.Error)
	}

	// Get the image to return the GCS object name
	var image NoteImage
	result = db.conn.WithContext(ctx).Where(`id = ? AND "noteId" = ?`, imageID, noteID).First(&image)
	if result.Error == gorm.ErrRecordNotFound {
		return "", nil // Image doesn't exist, nothing to delete
	}
	if result.Error != nil {
		return "", fmt.Errorf("failed to get image: %w", result.Error)
	}

	// Delete the image
	if err := db.conn.WithContext(ctx).Delete(&image).Error; err != nil {
		return "", fmt.Errorf("failed to delete image: %w", err)
	}

	return image.GCSObjectName, nil
}

// GetNoteImages retrieves all images for a note (public version)
func (db *DB) GetNoteImages(ctx context.Context, noteID string) ([]NoteImage, error) {
	return db.getNoteImages(ctx, noteID)
}

// GetImagesByNoteID retrieves images for a note for deletion purposes
func (db *DB) GetImagesByNoteID(ctx context.Context, noteID string) ([]NoteImage, error) {
	var images []NoteImage
	err := db.conn.WithContext(ctx).Where(`"noteId" = ?`, noteID).Find(&images).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get images: %w", err)
	}
	return images, nil
}

// AddAudioToNote adds an audio file to a note
func (db *DB) AddAudioToNote(ctx context.Context, noteID string, audio *NoteAudio) error {
	audio.NoteID = noteID
	if audio.CreatedAt.IsZero() {
		audio.CreatedAt = time.Now()
	}
	if err := db.conn.WithContext(ctx).Create(audio).Error; err != nil {
		return fmt.Errorf("failed to add audio to note: %w", err)
	}
	return nil
}

// RemoveAudioFromNote removes an audio file from a note and returns the GCS object name for cleanup
func (db *DB) RemoveAudioFromNote(ctx context.Context, userID, noteID, audioID string) (string, error) {
	// First verify the note belongs to the user
	var note Note
	result := db.conn.WithContext(ctx).Where(`id = ? AND "userId" = ?`, noteID, userID).First(&note)
	if result.Error == gorm.ErrRecordNotFound {
		return "", fmt.Errorf("note not found")
	}
	if result.Error != nil {
		return "", fmt.Errorf("failed to verify note ownership: %w", result.Error)
	}

	// Get the audio to return the GCS object name
	var audio NoteAudio
	result = db.conn.WithContext(ctx).Where(`id = ? AND "noteId" = ?`, audioID, noteID).First(&audio)
	if result.Error == gorm.ErrRecordNotFound {
		return "", nil // Audio doesn't exist, nothing to delete
	}
	if result.Error != nil {
		return "", fmt.Errorf("failed to get audio: %w", result.Error)
	}

	// Delete the audio
	if err := db.conn.WithContext(ctx).Delete(&audio).Error; err != nil {
		return "", fmt.Errorf("failed to delete audio: %w", err)
	}

	return audio.GCSObjectName, nil
}

// GetAudiosByNoteID retrieves audio files for a note for deletion purposes
func (db *DB) GetAudiosByNoteID(ctx context.Context, noteID string) ([]NoteAudio, error) {
	var audios []NoteAudio
	err := db.conn.WithContext(ctx).Where(`"noteId" = ?`, noteID).Find(&audios).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get audios: %w", err)
	}
	return audios, nil
}

// ListTags retrieves all tags for a user with usage counts
func (db *DB) ListTags(ctx context.Context, userID string) ([]Tag, error) {
	var tags []Tag
	err := db.conn.WithContext(ctx).
		Select(`"Tag".*, COUNT("NoteTag"."noteId") as count`).
		Joins(`LEFT JOIN "NoteTag" ON "Tag".id = "NoteTag"."tagId"`).
		Where(`"Tag"."userId" = ?`, userID).
		Group(`"Tag".id`).
		Order(`"Tag".name`).
		Find(&tags).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query tags: %w", err)
	}
	return tags, nil
}

// CreateUser creates a new user with email and password
func (db *DB) CreateUser(ctx context.Context, email, passwordHash string) (*User, error) {
	now := time.Now()
	user := User{
		ID:                 models.GenerateCUID(),
		Email:              email,
		PasswordHash:       passwordHash,
		SubscriptionStatus: "free",
		CreatedAt:          now,
	}

	if err := db.conn.WithContext(ctx).Create(&user).Error; err != nil {
		return nil, fmt.Errorf("failed to insert user: %w", err)
	}

	return &user, nil
}

// GetUserByEmail retrieves a user by email address
func (db *DB) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var user User
	result := db.conn.WithContext(ctx).Where("email = ?", email).First(&user)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get user: %w", result.Error)
	}
	return &user, nil
}

// GetUser retrieves a user by ID
func (db *DB) GetUser(ctx context.Context, userID string) (*User, error) {
	var user User
	result := db.conn.WithContext(ctx).Where("id = ?", userID).First(&user)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get user: %w", result.Error)
	}
	return &user, nil
}

// GetUserByStripeCustomerID retrieves a user by Stripe customer ID
func (db *DB) GetUserByStripeCustomerID(ctx context.Context, stripeCustomerID string) (*User, error) {
	var user User
	result := db.conn.WithContext(ctx).Where(`"stripeCustomerId" = ?`, stripeCustomerID).First(&user)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get user: %w", result.Error)
	}
	return &user, nil
}

// UpdateUserSubscription updates a user's subscription information
func (db *DB) UpdateUserSubscription(ctx context.Context, userID, subscriptionStatus string, stripeCustomerID *string, subscriptionEnd *time.Time) (*User, error) {
	updates := map[string]interface{}{
		"subscriptionStatus": subscriptionStatus,
	}
	if stripeCustomerID != nil {
		updates["stripeCustomerId"] = *stripeCustomerID
	}
	if subscriptionEnd != nil {
		updates["subscriptionEnd"] = *subscriptionEnd
	}

	result := db.conn.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Updates(updates)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to update user subscription: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}

	return db.GetUser(ctx, userID)
}

// CreateApiKey creates a new API key for a user
func (db *DB) CreateApiKey(ctx context.Context, userID, name, keyPrefix, keyHash string) (*ApiKey, error) {
	now := time.Now()
	apiKey := ApiKey{
		ID:        models.GenerateCUID(),
		Name:      name,
		KeyPrefix: keyPrefix,
		KeyHash:   keyHash,
		UserID:    userID,
		CreatedAt: now,
	}

	if err := db.conn.WithContext(ctx).Create(&apiKey).Error; err != nil {
		return nil, fmt.Errorf("failed to insert API key: %w", err)
	}

	return &apiKey, nil
}

// ListApiKeys retrieves all API keys for a user (without the hash)
func (db *DB) ListApiKeys(ctx context.Context, userID string) ([]ApiKey, error) {
	var keys []ApiKey
	err := db.conn.WithContext(ctx).
		Select(`id, name, "keyPrefix", "createdAt", "lastUsed", "userId"`).
		Where(`"userId" = ?`, userID).
		Order(`"createdAt" DESC`).
		Find(&keys).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query API keys: %w", err)
	}
	return keys, nil
}

// DeleteApiKey deletes an API key for a user
func (db *DB) DeleteApiKey(ctx context.Context, userID, keyID string) (bool, error) {
	result := db.conn.WithContext(ctx).Where(`id = ? AND "userId" = ?`, keyID, userID).Delete(&ApiKey{})
	if result.Error != nil {
		return false, fmt.Errorf("failed to delete API key: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// GetApiKeysByPrefix retrieves API keys by prefix for verification
func (db *DB) GetApiKeysByPrefix(ctx context.Context, keyPrefix string) ([]ApiKey, error) {
	var keys []ApiKey
	err := db.conn.WithContext(ctx).Where(`"keyPrefix" = ?`, keyPrefix).Find(&keys).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query API keys: %w", err)
	}
	return keys, nil
}

// UpdateApiKeyLastUsed updates the lastUsed timestamp for an API key
func (db *DB) UpdateApiKeyLastUsed(ctx context.Context, keyID string) error {
	return db.conn.WithContext(ctx).Model(&ApiKey{}).Where("id = ?", keyID).Update("lastUsed", time.Now()).Error
}

// GetNotesWithFewTags retrieves notes for a user that have fewer than maxTags tags
func (db *DB) GetNotesWithFewTags(ctx context.Context, userID string, maxTags int) ([]Note, error) {
	var notes []Note

	// Query to find notes with tag count less than maxTags
	err := db.conn.WithContext(ctx).
		Select(`"Note".*`).
		Joins(`LEFT JOIN "NoteTag" ON "Note".id = "NoteTag"."noteId"`).
		Where(`"Note"."userId" = ?`, userID).
		Group(`"Note".id`).
		Having("COUNT(\"NoteTag\".\"tagId\") < ?", maxTags).
		Order(`"Note"."createdAt" DESC`).
		Find(&notes).Error

	if err != nil {
		return nil, fmt.Errorf("failed to query notes with few tags: %w", err)
	}

	// Fetch tags for each note
	for i := range notes {
		tags, err := db.getNoteTags(ctx, notes[i].ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get tags for note %s: %w", notes[i].ID, err)
		}
		notes[i].Tags = tags
	}

	return notes, nil
}

// AddTagsToNote adds tags to a note without removing existing tags
func (db *DB) AddTagsToNote(ctx context.Context, userID, noteID string, tagNames []string) error {
	return db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Verify note ownership
		var note Note
		result := tx.Where(`id = ? AND "userId" = ?`, noteID, userID).First(&note)
		if result.Error == gorm.ErrRecordNotFound {
			return fmt.Errorf("note not found")
		}
		if result.Error != nil {
			return fmt.Errorf("failed to verify note ownership: %w", result.Error)
		}

		now := time.Now()
		tagsAdded := false

		// Add new tags
		for _, tagName := range tagNames {
			if tagName == "" {
				continue
			}

			// Find or create the tag
			var tag models.Tag
			result := tx.Where(`"userId" = ? AND name = ?`, userID, tagName).First(&tag)
			if result.Error == gorm.ErrRecordNotFound {
				tag = models.Tag{
					ID:        models.GenerateCUID(),
					Name:      tagName,
					CreatedAt: now,
					UserID:    userID,
				}
				if err := tx.Create(&tag).Error; err != nil {
					return fmt.Errorf("failed to create tag: %w", err)
				}
			} else if result.Error != nil {
				return result.Error
			}

			// Check if the tag is already linked to the note
			var noteTag models.NoteTag
			result = tx.Where(`"noteId" = ? AND "tagId" = ?`, noteID, tag.ID).First(&noteTag)
			if result.Error == gorm.ErrRecordNotFound {
				// Link note to tag if not already linked
				noteTag = models.NoteTag{NoteID: noteID, TagID: tag.ID}
				if err := tx.Create(&noteTag).Error; err != nil {
					return fmt.Errorf("failed to link note to tag: %w", err)
				}
				tagsAdded = true
			} else if result.Error != nil {
				return result.Error
			}
		}

		// Update the note's updatedAt timestamp if tags were added
		if tagsAdded {
			if err := tx.Model(&note).Update("updatedAt", now).Error; err != nil {
				return fmt.Errorf("failed to update note timestamp: %w", err)
			}
		}

		return nil
	})
}

// GetUserSettings retrieves user settings for a user
func (db *DB) GetUserSettings(ctx context.Context, userID string) (*User, error) {
	var user User
	result := db.conn.WithContext(ctx).Where(`"id" = ?`, userID).First(&user)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get user: %w", result.Error)
	}
	return &user, nil
}

// UpdateUserSettings updates or creates user settings
func (db *DB) UpdateUserSettings(ctx context.Context, userID string, notionKey, name, image, password *string) (*User, error) {
	now := time.Now()

	var user User
	result := db.conn.WithContext(ctx).Where(`"id" = ?`, userID).First(&user)

	if result.Error == gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("user not found")
	} else if result.Error != nil {
		return nil, fmt.Errorf("failed to get user: %w", result.Error)
	}

	// Update user fields
	updates := map[string]interface{}{
		"updatedAt": now,
	}
	if notionKey != nil {
		updates["notionKey"] = *notionKey
	}
	if name != nil {
		updates["name"] = *name
	}
	if image != nil {
		updates["image"] = *image
	}
	if password != nil && *password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("failed to hash password: %w", err)
		}
		updates["passwordHash"] = string(hash)
	}

	if err := db.conn.WithContext(ctx).Model(&user).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	// Reload to get updated values
	if err := db.conn.WithContext(ctx).Where(`"id" = ?`, userID).First(&user).Error; err != nil {
		return nil, fmt.Errorf("failed to reload user: %w", err)
	}

	return &user, nil
}

// GetUsersWithNotionKeys retrieves all users who have a Notion API key configured
func (db *DB) GetUsersWithNotionKeys(ctx context.Context) ([]User, error) {
	var users []User
	err := db.conn.WithContext(ctx).
		Where(`"notionKey" IS NOT NULL AND "notionKey" != ''`).
		Find(&users).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query users with Notion keys: %w", err)
	}
	return users, nil
}

// ListAllUsers retrieves all users
func (db *DB) ListAllUsers(ctx context.Context) ([]User, error) {
	var users []User
	err := db.conn.WithContext(ctx).Find(&users).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	return users, nil
}

// GetRandomNotes retrieves a random set of notes for a user
func (db *DB) GetRandomNotes(ctx context.Context, userID string, count int) ([]Note, error) {
	if count <= 0 {
		count = 5 // Default to 5 notes
	}

	var notes []Note
	// Use ORDER BY RANDOM() to get random notes from PostgreSQL
	err := db.conn.WithContext(ctx).
		Where(`"userId" = ?`, userID).
		Order("RANDOM()").
		Limit(count).
		Find(&notes).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query random notes: %w", err)
	}

	if len(notes) == 0 {
		return notes, nil
	}

	// Collect note IDs for batch fetching
	noteIDs := make([]string, len(notes))
	for i, n := range notes {
		noteIDs[i] = n.ID
	}

	// Batch fetch tags for all notes
	tagsByNoteID, err := db.getTagsForNotes(ctx, noteIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to batch fetch tags: %w", err)
	}

	// Batch fetch images for all notes
	imagesByNoteID, err := db.getImagesForNotes(ctx, noteIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to batch fetch images: %w", err)
	}

	// Assign tags and images to notes
	for i := range notes {
		notes[i].Tags = tagsByNoteID[notes[i].ID]
		notes[i].Images = imagesByNoteID[notes[i].ID]
	}

	return notes, nil
}

// parseTagSearch extracts tag:tagname patterns from a search string.
// Returns the extracted tag names and the remaining search text.
var tagSearchRegex = regexp.MustCompile(`\btag:([a-z0-9]+)\b`)

func parseTagSearch(search string) (tags []string, remaining string) {
	matches := tagSearchRegex.FindAllStringSubmatch(search, -1)
	for _, match := range matches {
		if len(match) > 1 {
			tags = append(tags, match[1])
		}
	}

	// Remove the tag: patterns from the search string
	remaining = tagSearchRegex.ReplaceAllString(search, "")
	remaining = strings.TrimSpace(remaining)
	// Clean up multiple spaces
	remaining = regexp.MustCompile(`\s+`).ReplaceAllString(remaining, " ")

	return tags, remaining
}
