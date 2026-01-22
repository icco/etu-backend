package service

import (
	"context"

	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	MaxNotesLimit     = 100
	DefaultNotesLimit = 50
)

// NotesService implements the NotesService gRPC service
type NotesService struct {
	pb.UnimplementedNotesServiceServer
	db *db.DB
}

// NewNotesService creates a new NotesService
func NewNotesService(database *db.DB) *NotesService {
	return &NotesService{db: database}
}

// ListNotes retrieves notes for a user with optional filtering
func (s *NotesService) ListNotes(ctx context.Context, req *pb.ListNotesRequest) (*pb.ListNotesResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = DefaultNotesLimit
	}
	if limit > MaxNotesLimit {
		limit = MaxNotesLimit
	}

	offset := int(req.Offset)
	if offset < 0 {
		offset = 0
	}

	notes, total, err := s.db.ListNotes(ctx, req.UserId, req.Search, req.Tags, req.StartDate, req.EndDate, limit, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list notes: %v", err)
	}

	pbNotes := make([]*pb.Note, len(notes))
	for i, n := range notes {
		pbNotes[i] = noteToProto(&n)
	}

	return &pb.ListNotesResponse{
		Notes:  pbNotes,
		Total:  int32(total),
		Limit:  int32(limit),
		Offset: int32(offset),
	}, nil
}

// CreateNote creates a new note
func (s *NotesService) CreateNote(ctx context.Context, req *pb.CreateNoteRequest) (*pb.CreateNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Content == "" {
		return nil, status.Error(codes.InvalidArgument, "content is required")
	}

	note, err := s.db.CreateNote(ctx, req.UserId, req.Content, req.Tags)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create note: %v", err)
	}

	return &pb.CreateNoteResponse{
		Note: noteToProto(note),
	}, nil
}

// GetNote retrieves a single note by ID
func (s *NotesService) GetNote(ctx context.Context, req *pb.GetNoteRequest) (*pb.GetNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	note, err := s.db.GetNote(ctx, req.UserId, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get note: %v", err)
	}
	if note == nil {
		return nil, status.Error(codes.NotFound, "note not found")
	}

	return &pb.GetNoteResponse{
		Note: noteToProto(note),
	}, nil
}

// UpdateNote updates an existing note
func (s *NotesService) UpdateNote(ctx context.Context, req *pb.UpdateNoteRequest) (*pb.UpdateNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	var content *string
	if req.Content != nil {
		content = req.Content
	}

	note, err := s.db.UpdateNote(ctx, req.UserId, req.Id, content, req.Tags, req.UpdateTags)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update note: %v", err)
	}
	if note == nil {
		return nil, status.Error(codes.NotFound, "note not found")
	}

	return &pb.UpdateNoteResponse{
		Note: noteToProto(note),
	}, nil
}

// DeleteNote deletes a note by ID
func (s *NotesService) DeleteNote(ctx context.Context, req *pb.DeleteNoteRequest) (*pb.DeleteNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	deleted, err := s.db.DeleteNote(ctx, req.UserId, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete note: %v", err)
	}

	return &pb.DeleteNoteResponse{
		Success: deleted,
	}, nil
}

// noteToProto converts a db.Note to a protobuf Note
func noteToProto(n *db.Note) *pb.Note {
	return &pb.Note{
		Id:        n.ID,
		Content:   n.Content,
		Tags:      n.Tags,
		CreatedAt: &pb.Timestamp{Seconds: n.CreatedAt.Unix(), Nanos: int32(n.CreatedAt.Nanosecond())},
		UpdatedAt: &pb.Timestamp{Seconds: n.UpdatedAt.Unix(), Nanos: int32(n.UpdatedAt.Nanosecond())},
	}
}
