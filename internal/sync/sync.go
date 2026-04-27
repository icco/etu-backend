package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/icco/etu-backend/internal/notion"
	"github.com/icco/etu-backend/internal/syncdb"
	"github.com/icco/gutil/logging"
	"go.uber.org/zap"
)

// Syncer handles syncing between Notion and PostgreSQL.
// Loggers are sourced from the per-call ctx via gutil/logging.
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
	l := logging.FromContext(ctx).With("user_id", userID)

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
			l.Infow("no previous sync found, performing full sync")
			posts, err = s.notion.ListAllPosts(ctx)
		} else {
			// Buffer the cutoff slightly to avoid missing edits whose modification
			// timestamps happen to land within the same second as last sync.
			since := lastSync.Add(-5 * time.Minute)
			l.Infow("starting incremental sync", "since", since.Format(time.RFC3339))
			posts, err = s.notion.ListPostsSince(ctx, since)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to fetch posts from Notion: %w", err)
	}

	l.Infow("fetched posts from Notion", "count", len(posts))

	for _, post := range posts {
		existing, getErr := s.db.GetNoteByNotionUUID(userID, post.ID)
		if getErr != nil {
			l.Errorw("error checking existing note", "notion_uuid", post.ID, zap.Error(getErr))
			result.Errors++
			continue
		}

		_, isNew, upsertErr := s.db.UpsertNoteFromNotion(
			userID,
			post.ID,
			post.PageID,
			post.Text,
			post.Tags,
			post.CreatedAt,
			post.ModifiedAt,
		)
		if upsertErr != nil {
			l.Errorw("error upserting note", "notion_uuid", post.ID, zap.Error(upsertErr))
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

	if err := s.db.UpdateLastSyncTime(userID, time.Now()); err != nil {
		l.Warnw("failed to update last sync time", zap.Error(err))
	}

	result.Duration = time.Since(start)
	return result, nil
}

// tagsChanged checks if tags have changed for a note
func (s *Syncer) tagsChanged(noteID string, newTags []string) bool {
	existingTags, err := s.db.GetNoteTags(noteID)
	if err != nil {
		// Assume changed when we cannot reliably compare; this favors a write
		// over silently skipping a real edit.
		return true
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
	l := logging.FromContext(ctx).With("user_id", userID)

	start := time.Now()
	result := &SyncToNotionResult{}

	notes, err := s.db.GetNotesNeedingSyncToNotion(userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get notes needing sync: %w", err)
	}

	l.Infow("syncing notes to Notion", "count", len(notes))

	for _, note := range notes {
		tags, tagErr := s.db.GetNoteTags(note.ID)
		if tagErr != nil {
			l.Errorw("error getting tags for note", "note_id", note.ID, zap.Error(tagErr))
			result.Errors++
			continue
		}

		if note.ExternalID == nil || *note.ExternalID == "" {
			pageID, createErr := s.notion.CreatePost(ctx, note.ID, note.Content, tags)
			if createErr != nil {
				l.Errorw("error creating Notion page", "note_id", note.ID, zap.Error(createErr))
				result.Errors++
				continue
			}

			if markErr := s.db.MarkNoteSyncedToNotion(note.ID, pageID, note.ID); markErr != nil {
				l.Errorw("error marking note as synced", "note_id", note.ID, zap.Error(markErr))
				result.Errors++
				continue
			}

			result.Created++
			l.Infow("created Notion page", "note_id", note.ID, "page_id", pageID)
		} else {
			if updateErr := s.notion.UpdatePost(ctx, *note.ExternalID, note.Content, tags); updateErr != nil {
				l.Errorw("error updating Notion page", "note_id", note.ID, "page_id", *note.ExternalID, zap.Error(updateErr))
				result.Errors++
				continue
			}

			if markErr := s.db.UpdateNoteNotionSyncTime(note.ID); markErr != nil {
				l.Errorw("error updating sync time", "note_id", note.ID, zap.Error(markErr))
				result.Errors++
				continue
			}

			result.Updated++
			l.Infow("updated Notion page", "note_id", note.ID, "page_id", *note.ExternalID)
		}
	}

	archivedPageIDs, err := s.db.GetArchivedNotePageIDs(userID)
	if err != nil {
		l.Warnw("failed to get archived notes", zap.Error(err))
	} else {
		for _, pageID := range archivedPageIDs {
			if archiveErr := s.notion.ArchivePost(ctx, pageID); archiveErr != nil {
				l.Errorw("error archiving Notion page", "page_id", pageID, zap.Error(archiveErr))
				result.Errors++
				continue
			}
			result.Archived++
			l.Infow("archived Notion page", "page_id", pageID)
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// SyncUserBidirectional performs a full bidirectional sync for a user.
// It first syncs from Notion to the local DB, then syncs local changes back to Notion.
func (s *Syncer) SyncUserBidirectional(ctx context.Context, userID string, fullSync bool) (*SyncResult, *SyncToNotionResult, error) {
	fromNotionResult, err := s.SyncUser(ctx, userID, fullSync)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sync from Notion: %w", err)
	}

	toNotionResult, err := s.SyncUserToNotion(ctx, userID)
	if err != nil {
		return fromNotionResult, nil, fmt.Errorf("failed to sync to Notion: %w", err)
	}

	return fromNotionResult, toNotionResult, nil
}
