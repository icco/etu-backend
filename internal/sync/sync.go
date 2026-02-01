package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/icco/etu-backend/internal/notion"
	"github.com/icco/etu-backend/internal/syncdb"
)

// Syncer handles syncing between Notion and PostgreSQL.
type Syncer struct {
	db     *syncdb.DB
	notion *notion.Client
	log    *slog.Logger
}

// NewSyncer creates a new Syncer instance.
func NewSyncer(database *syncdb.DB, notionClient *notion.Client) *Syncer {
	return &Syncer{
		db:     database,
		notion: notionClient,
		log:    slog.Default(),
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

// SyncToNotionResult contains statistics from syncing back to Notion.
type SyncToNotionResult struct {
	Created  int
	Updated  int
	Archived int
	Errors   int
	Duration time.Duration
}

// SyncUser syncs all Notion posts for a specific user to the database.
// If fullSync is true, it fetches all posts; otherwise it only fetches posts modified since last sync.
func (s *Syncer) SyncUser(ctx context.Context, userID string, fullSync bool) (*SyncResult, error) {
	start := time.Now()
	result := &SyncResult{}

	var posts []*notion.Post
	var err error

	if fullSync {
		posts, err = s.notion.ListAllPosts(ctx)
	} else {
		lastSync, syncErr := s.db.GetLastSyncTime(userID)
		if syncErr != nil {
			return nil, fmt.Errorf("failed to get last sync time: %w", syncErr)
		}

		if lastSync == nil {
			s.log.Info("no previous sync found, performing full sync", "user_id", userID)
			posts, err = s.notion.ListAllPosts(ctx)
		} else {
			// Add a small buffer to avoid missing posts due to timing
			since := lastSync.Add(-5 * time.Minute)
			s.log.Info("starting incremental sync", "user_id", userID, "since", since.Format(time.RFC3339))
			posts, err = s.notion.ListPostsSince(ctx, since)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to fetch posts from Notion: %w", err)
	}

	s.log.Info("fetched posts from Notion", "user_id", userID, "count", len(posts))

	for _, post := range posts {
		// Get existing note to check if it changed
		existing, getErr := s.db.GetNoteByNotionUUID(userID, post.ID)
		if getErr != nil {
			s.log.Error("error checking existing note", "notion_uuid", post.ID, "error", getErr)
			result.Errors++
			continue
		}

		// Upsert the note
		_, isNew, upsertErr := s.db.UpsertNoteFromNotion(
			userID,
			post.ID,     // Notion UUID (stored in ID property)
			post.PageID, // Notion page ID
			post.Text,
			post.Tags,
			post.CreatedAt,
			post.ModifiedAt,
		)
		if upsertErr != nil {
			s.log.Error("error upserting note", "notion_uuid", post.ID, "error", upsertErr)
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
		s.log.Warn("failed to update last sync time", "user_id", userID, "error", err)
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

// SyncUserToNotion syncs local changes back to Notion for a specific user.
// It creates new pages for notes without a Notion page ID, and updates
// existing pages for notes that have been modified locally.
func (s *Syncer) SyncUserToNotion(ctx context.Context, userID string) (*SyncToNotionResult, error) {
	start := time.Now()
	result := &SyncToNotionResult{}

	// Get notes that need to be synced to Notion
	notes, err := s.db.GetNotesNeedingSyncToNotion(userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get notes needing sync: %w", err)
	}

	s.log.Info("syncing notes to Notion", "user_id", userID, "count", len(notes))

	for _, note := range notes {
		// Get tags for this note
		tags, tagErr := s.db.GetNoteTags(note.ID)
		if tagErr != nil {
			s.log.Error("error getting tags for note", "note_id", note.ID, "error", tagErr)
			result.Errors++
			continue
		}

		if note.ExternalID == nil || *note.ExternalID == "" {
			// Note doesn't exist in Notion yet - create it
			pageID, createErr := s.notion.CreatePost(ctx, note.ID, note.Content, tags)
			if createErr != nil {
				s.log.Error("error creating Notion page", "note_id", note.ID, "error", createErr)
				result.Errors++
				continue
			}

			// Update the note with the new Notion page ID
			if markErr := s.db.MarkNoteSyncedToNotion(note.ID, pageID, note.ID); markErr != nil {
				s.log.Error("error marking note as synced", "note_id", note.ID, "error", markErr)
				result.Errors++
				continue
			}

			result.Created++
			s.log.Info("created Notion page", "note_id", note.ID, "page_id", pageID)
		} else {
			// Note exists in Notion - update it
			if updateErr := s.notion.UpdatePost(ctx, *note.ExternalID, note.Content, tags); updateErr != nil {
				s.log.Error("error updating Notion page", "note_id", note.ID, "page_id", *note.ExternalID, "error", updateErr)
				result.Errors++
				continue
			}

			// Update the sync timestamp
			if markErr := s.db.UpdateNoteNotionSyncTime(note.ID); markErr != nil {
				s.log.Error("error updating sync time", "note_id", note.ID, "error", markErr)
				result.Errors++
				continue
			}

			result.Updated++
			s.log.Info("updated Notion page", "note_id", note.ID, "page_id", *note.ExternalID)
		}
	}

	// Handle archived/deleted notes (archive them in Notion)
	archivedPageIDs, err := s.db.GetArchivedNotePageIDs(userID)
	if err != nil {
		s.log.Warn("failed to get archived notes", "user_id", userID, "error", err)
	} else {
		for _, pageID := range archivedPageIDs {
			if archiveErr := s.notion.ArchivePost(ctx, pageID); archiveErr != nil {
				s.log.Error("error archiving Notion page", "page_id", pageID, "error", archiveErr)
				result.Errors++
				continue
			}
			result.Archived++
			s.log.Info("archived Notion page", "page_id", pageID)
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// SyncUserBidirectional performs a full bidirectional sync for a user.
// It first syncs from Notion to the local DB, then syncs local changes back to Notion.
func (s *Syncer) SyncUserBidirectional(ctx context.Context, userID string, fullSync bool) (*SyncResult, *SyncToNotionResult, error) {
	// First, sync from Notion to local DB
	fromNotionResult, err := s.SyncUser(ctx, userID, fullSync)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sync from Notion: %w", err)
	}

	// Then, sync local changes back to Notion
	toNotionResult, err := s.SyncUserToNotion(ctx, userID)
	if err != nil {
		return fromNotionResult, nil, fmt.Errorf("failed to sync to Notion: %w", err)
	}

	return fromNotionResult, toNotionResult, nil
}
