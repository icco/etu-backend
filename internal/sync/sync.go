package sync

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/icco/etu-backend/internal/notion"
	pb "github.com/icco/etu-backend/proto"
)

// Syncer handles syncing between Notion and the backend via gRPC.
type Syncer struct {
	syncClient  pb.SyncServiceClient
	notesClient pb.NotesServiceClient
	notion      *notion.Client
}

// NewSyncer creates a new Syncer instance.
func NewSyncer(syncClient pb.SyncServiceClient, notesClient pb.NotesServiceClient, notionClient *notion.Client) *Syncer {
	return &Syncer{
		syncClient:  syncClient,
		notesClient: notesClient,
		notion:      notionClient,
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
		log.Printf("Starting full sync for user %s", userID)
		posts, err = s.notion.ListAllPosts(ctx)
	} else {
		resp, syncErr := s.syncClient.GetSyncState(ctx, &pb.GetSyncStateRequest{UserId: userID})
		if syncErr != nil {
			return nil, fmt.Errorf("failed to get last sync time: %w", syncErr)
		}

		if resp.LastSyncedAt == nil {
			log.Printf("No previous sync found for user %s, performing full sync", userID)
			posts, err = s.notion.ListAllPosts(ctx)
		} else {
			// Add a small buffer to avoid missing posts due to timing
			lastSync := time.Unix(resp.LastSyncedAt.Seconds, int64(resp.LastSyncedAt.Nanos))
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
		existingResp, getErr := s.notesClient.GetNoteByNotionUUID(ctx, &pb.GetNoteByNotionUUIDRequest{
			UserId:     userID,
			NotionUuid: post.ID,
		})
		if getErr != nil {
			log.Printf("Error checking existing note for %s: %v", post.ID, getErr)
			result.Errors++
			continue
		}

		// Upsert the note
		upsertResp, upsertErr := s.syncClient.UpsertNoteFromNotion(ctx, &pb.UpsertNoteFromNotionRequest{
			UserId:     userID,
			NotionUuid: post.ID,
			PageId:     post.PageID,
			Content:    post.Text,
			Tags:       post.Tags,
			CreatedAt:  &pb.Timestamp{Seconds: post.CreatedAt.Unix(), Nanos: int32(post.CreatedAt.Nanosecond())},
			UpdatedAt:  &pb.Timestamp{Seconds: post.ModifiedAt.Unix(), Nanos: int32(post.ModifiedAt.Nanosecond())},
		})
		if upsertErr != nil {
			log.Printf("Error upserting note %s: %v", post.ID, upsertErr)
			result.Errors++
			continue
		}

		if upsertResp.IsNew {
			result.Created++
		} else if existingResp.Note != nil && (existingResp.Note.Content != post.Text || !s.tagsMatch(existingResp.Note.Tags, post.Tags)) {
			result.Updated++
		} else {
			result.Unchanged++
		}
	}

	// Update last sync time
	now := time.Now()
	_, err = s.syncClient.UpdateSyncState(ctx, &pb.UpdateSyncStateRequest{
		UserId:       userID,
		LastSyncedAt: &pb.Timestamp{Seconds: now.Unix(), Nanos: int32(now.Nanosecond())},
	})
	if err != nil {
		log.Printf("Warning: failed to update last sync time: %v", err)
	}

	result.Duration = time.Since(start)
	return result, nil
}

// tagsMatch checks if two tag slices are equal
func (s *Syncer) tagsMatch(existing, new []string) bool {
	if len(existing) != len(new) {
		return false
	}

	tagMap := make(map[string]bool)
	for _, t := range existing {
		tagMap[t] = true
	}
	for _, t := range new {
		if !tagMap[t] {
			return false
		}
	}
	return true
}

// SyncUserToNotion syncs local changes back to Notion for a specific user.
// It creates new pages for notes without a Notion page ID, and updates
// existing pages for notes that have been modified locally.
func (s *Syncer) SyncUserToNotion(ctx context.Context, userID string) (*SyncToNotionResult, error) {
	start := time.Now()
	result := &SyncToNotionResult{}

	// Get notes that need to be synced to Notion
	resp, err := s.syncClient.GetNotesNeedingSyncToNotion(ctx, &pb.GetNotesNeedingSyncToNotionRequest{
		UserId: userID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get notes needing sync: %w", err)
	}

	log.Printf("Found %d notes to sync to Notion for user %s", len(resp.Notes), userID)

	for _, note := range resp.Notes {
		if note.ExternalId == nil || *note.ExternalId == "" {
			// Note doesn't exist in Notion yet - create it
			pageID, createErr := s.notion.CreatePost(ctx, note.Id, note.Content, note.Tags)
			if createErr != nil {
				log.Printf("Error creating Notion page for note %s: %v", note.Id, createErr)
				result.Errors++
				continue
			}

			// Update the note with the new Notion page ID
			_, markErr := s.syncClient.MarkNoteSyncedToNotion(ctx, &pb.MarkNoteSyncedToNotionRequest{
				NoteId:     note.Id,
				PageId:     pageID,
				NotionUuid: note.Id, // Use note ID as Notion UUID for new notes
			})
			if markErr != nil {
				log.Printf("Error marking note %s as synced: %v", note.Id, markErr)
				result.Errors++
				continue
			}

			result.Created++
			log.Printf("Created Notion page %s for note %s", pageID, note.Id)
		} else {
			// Note exists in Notion - update it
			if updateErr := s.notion.UpdatePost(ctx, *note.ExternalId, note.Content, note.Tags); updateErr != nil {
				log.Printf("Error updating Notion page %s for note %s: %v", *note.ExternalId, note.Id, updateErr)
				result.Errors++
				continue
			}

			// Update the sync timestamp
			_, markErr := s.syncClient.UpdateNoteSyncTime(ctx, &pb.UpdateNoteSyncTimeRequest{
				NoteId: note.Id,
			})
			if markErr != nil {
				log.Printf("Error updating sync time for note %s: %v", note.Id, markErr)
				result.Errors++
				continue
			}

			result.Updated++
			log.Printf("Updated Notion page %s for note %s", *note.ExternalId, note.Id)
		}
	}

	// Note: Archived notes handling would go here if implemented

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
