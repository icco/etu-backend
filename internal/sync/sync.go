package sync

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/icco/etu-backend/internal/notion"
	"github.com/icco/etu-backend/internal/syncdb"
)

// Syncer handles syncing between Notion and PostgreSQL.
type Syncer struct {
	db     *syncdb.DB
	notion *notion.Client
}

// NewSyncer creates a new Syncer instance.
func NewSyncer(database *syncdb.DB, notionClient *notion.Client) *Syncer {
	return &Syncer{
		db:     database,
		notion: notionClient,
	}
}

// SyncResult contains statistics from a sync operation.
type SyncResult struct {
	Created   int
	Updated   int
	Unchanged int
	Errors    int
	Duration  time.Duration
}

// SyncUser syncs all Notion posts for a specific user to the database.
// If fullSync is true, it fetches all posts; otherwise it only fetches posts modified since last sync.
func (s *Syncer) SyncUser(ctx context.Context, userID string, fullSync bool) (*SyncResult, error) {
	start := time.Now()
	result := &SyncResult{}

	var posts []*notion.Post
	var err error

	if fullSync {
		log.Printf("Starting full sync for user %s", userID)
		posts, err = s.notion.ListAllPosts(ctx)
	} else {
		lastSync, syncErr := s.db.GetLastSyncTime(userID)
		if syncErr != nil {
			return nil, fmt.Errorf("failed to get last sync time: %w", syncErr)
		}

		if lastSync == nil {
			log.Printf("No previous sync found for user %s, performing full sync", userID)
			posts, err = s.notion.ListAllPosts(ctx)
		} else {
			// Add a small buffer to avoid missing posts due to timing
			since := lastSync.Add(-5 * time.Minute)
			log.Printf("Starting incremental sync for user %s (since %s)", userID, since.Format(time.RFC3339))
			posts, err = s.notion.ListPostsSince(ctx, since)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to fetch posts from Notion: %w", err)
	}

	log.Printf("Fetched %d posts from Notion", len(posts))

	for _, post := range posts {
		// Get existing note to check if it changed
		existing, getErr := s.db.GetNoteByNotionUUID(userID, post.ID)
		if getErr != nil {
			log.Printf("Error checking existing note for %s: %v", post.ID, getErr)
			result.Errors++
			continue
		}

		// Upsert the note
		_, isNew, upsertErr := s.db.UpsertNoteFromNotion(
			userID,
			post.ID,       // Notion UUID (stored in ID property)
			post.PageID,   // Notion page ID
			post.Text,
			post.Tags,
			post.CreatedAt,
			post.ModifiedAt,
		)
		if upsertErr != nil {
			log.Printf("Error upserting note %s: %v", post.ID, upsertErr)
			result.Errors++
			continue
		}

		if isNew {
			result.Created++
		} else if existing != nil && (existing.Content != post.Text || !s.tagsChanged(existing.ID, post.Tags)) {
			result.Updated++
		} else {
			result.Unchanged++
		}
	}

	// Update last sync time
	if err := s.db.UpdateLastSyncTime(userID, time.Now()); err != nil {
		log.Printf("Warning: failed to update last sync time: %v", err)
	}

	result.Duration = time.Since(start)
	return result, nil
}

// tagsChanged checks if tags have changed for a note
func (s *Syncer) tagsChanged(noteID string, newTags []string) bool {
	existingTags, err := s.db.GetNoteTags(noteID)
	if err != nil {
		return true // Assume changed if we can't check
	}

	if len(existingTags) != len(newTags) {
		return true
	}

	tagMap := make(map[string]bool)
	for _, t := range existingTags {
		tagMap[t] = true
	}
	for _, t := range newTags {
		if !tagMap[t] {
			return true
		}
	}
	return false
}
