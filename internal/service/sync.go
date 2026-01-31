package service

import (
	"context"
	"time"

	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SyncService implements the SyncService gRPC service
type SyncService struct {
	pb.UnimplementedSyncServiceServer
	db *db.DB
}

// NewSyncService creates a new SyncService
func NewSyncService(database *db.DB) *SyncService {
	return &SyncService{db: database}
}

// GetSyncState returns the last sync time for a user
func (s *SyncService) GetSyncState(ctx context.Context, req *pb.GetSyncStateRequest) (*pb.GetSyncStateResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Verify authorization
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	lastSynced, err := s.db.GetLastSyncTime(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get sync state: %v", err)
	}

	var ts *pb.Timestamp
	if lastSynced != nil {
		ts = &pb.Timestamp{
			Seconds: lastSynced.Unix(),
			Nanos:   int32(lastSynced.Nanosecond()),
		}
	}

	return &pb.GetSyncStateResponse{
		LastSyncedAt: ts,
	}, nil
}

// UpdateSyncState updates the last sync time for a user
func (s *SyncService) UpdateSyncState(ctx context.Context, req *pb.UpdateSyncStateRequest) (*pb.UpdateSyncStateResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.LastSyncedAt == nil {
		return nil, status.Error(codes.InvalidArgument, "last_synced_at is required")
	}

	// Verify authorization
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	syncTime := time.Unix(req.LastSyncedAt.Seconds, int64(req.LastSyncedAt.Nanos))
	if err := s.db.UpdateLastSyncTime(ctx, req.UserId, syncTime); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update sync state: %v", err)
	}

	return &pb.UpdateSyncStateResponse{
		Success: true,
	}, nil
}

// UpsertNoteFromNotion creates or updates a note from Notion data
func (s *SyncService) UpsertNoteFromNotion(ctx context.Context, req *pb.UpsertNoteFromNotionRequest) (*pb.UpsertNoteFromNotionResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.NotionUuid == "" {
		return nil, status.Error(codes.InvalidArgument, "notion_uuid is required")
	}
	if req.PageId == "" {
		return nil, status.Error(codes.InvalidArgument, "page_id is required")
	}

	// Verify authorization
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	var createdAt, updatedAt time.Time
	if req.CreatedAt != nil {
		createdAt = time.Unix(req.CreatedAt.Seconds, int64(req.CreatedAt.Nanos))
	} else {
		createdAt = time.Now()
	}
	if req.UpdatedAt != nil {
		updatedAt = time.Unix(req.UpdatedAt.Seconds, int64(req.UpdatedAt.Nanos))
	} else {
		updatedAt = time.Now()
	}

	note, isNew, err := s.db.UpsertNoteFromNotion(ctx, req.UserId, req.NotionUuid, req.PageId, req.Content, req.Tags, createdAt, updatedAt)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to upsert note: %v", err)
	}

	return &pb.UpsertNoteFromNotionResponse{
		Note:  noteToProto(note),
		IsNew: isNew,
	}, nil
}

// GetNotesNeedingSyncToNotion returns notes that need to be synced to Notion
func (s *SyncService) GetNotesNeedingSyncToNotion(ctx context.Context, req *pb.GetNotesNeedingSyncToNotionRequest) (*pb.GetNotesNeedingSyncToNotionResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Verify authorization
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	notes, err := s.db.GetNotesNeedingSyncToNotion(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get notes needing sync: %v", err)
	}

	pbNotes := make([]*pb.Note, len(notes))
	for i := range notes {
		pbNotes[i] = noteToProto(&notes[i])
	}

	return &pb.GetNotesNeedingSyncToNotionResponse{
		Notes: pbNotes,
	}, nil
}

// MarkNoteSyncedToNotion updates a note's Notion sync status
func (s *SyncService) MarkNoteSyncedToNotion(ctx context.Context, req *pb.MarkNoteSyncedToNotionRequest) (*pb.MarkNoteSyncedToNotionResponse, error) {
	if req.NoteId == "" {
		return nil, status.Error(codes.InvalidArgument, "note_id is required")
	}
	if req.PageId == "" {
		return nil, status.Error(codes.InvalidArgument, "page_id is required")
	}
	if req.NotionUuid == "" {
		return nil, status.Error(codes.InvalidArgument, "notion_uuid is required")
	}

	// Note: This endpoint requires M2M auth since it doesn't have a user_id in the request

	if err := s.db.MarkNoteSyncedToNotion(ctx, req.NoteId, req.PageId, req.NotionUuid); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to mark note synced: %v", err)
	}

	return &pb.MarkNoteSyncedToNotionResponse{
		Success: true,
	}, nil
}

// UpdateNoteSyncTime updates a note's lastSyncedToNotion timestamp
func (s *SyncService) UpdateNoteSyncTime(ctx context.Context, req *pb.UpdateNoteSyncTimeRequest) (*pb.UpdateNoteSyncTimeResponse, error) {
	if req.NoteId == "" {
		return nil, status.Error(codes.InvalidArgument, "note_id is required")
	}

	// Note: This endpoint requires M2M auth since it doesn't have a user_id in the request

	if err := s.db.UpdateNoteSyncTime(ctx, req.NoteId); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update sync time: %v", err)
	}

	return &pb.UpdateNoteSyncTimeResponse{
		Success: true,
	}, nil
}
