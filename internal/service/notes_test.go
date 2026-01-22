package service

import (
	"context"
	"testing"
	"time"

	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockNotesService wraps NotesService for testing
type mockNotesService struct {
	pb.UnimplementedNotesServiceServer
	notes map[string]*db.Note
}

func newMockNotesService() *mockNotesService {
	return &mockNotesService{
		notes: make(map[string]*db.Note),
	}
}

func (s *mockNotesService) CreateNote(ctx context.Context, req *pb.CreateNoteRequest) (*pb.CreateNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Content == "" {
		return nil, status.Error(codes.InvalidArgument, "content is required")
	}

	now := time.Now()
	note := &db.Note{
		ID:        "test-note-id",
		Content:   req.Content,
		CreatedAt: now,
		UpdatedAt: now,
		UserID:    req.UserId,
		Tags:      req.Tags,
	}
	s.notes[note.ID] = note

	return &pb.CreateNoteResponse{
		Note: noteToProto(note),
	}, nil
}

func (s *mockNotesService) GetNote(ctx context.Context, req *pb.GetNoteRequest) (*pb.GetNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	note, ok := s.notes[req.Id]
	if !ok || note.UserID != req.UserId {
		return nil, status.Error(codes.NotFound, "note not found")
	}

	return &pb.GetNoteResponse{
		Note: noteToProto(note),
	}, nil
}

func (s *mockNotesService) ListNotes(ctx context.Context, req *pb.ListNotesRequest) (*pb.ListNotesResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	var notes []*pb.Note
	for _, n := range s.notes {
		if n.UserID == req.UserId {
			notes = append(notes, noteToProto(n))
		}
	}

	return &pb.ListNotesResponse{
		Notes:  notes,
		Total:  int32(len(notes)),
		Limit:  DefaultNotesLimit,
		Offset: 0,
	}, nil
}

func (s *mockNotesService) DeleteNote(ctx context.Context, req *pb.DeleteNoteRequest) (*pb.DeleteNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	note, ok := s.notes[req.Id]
	if !ok || note.UserID != req.UserId {
		return &pb.DeleteNoteResponse{Success: false}, nil
	}

	delete(s.notes, req.Id)
	return &pb.DeleteNoteResponse{Success: true}, nil
}

func TestCreateNote(t *testing.T) {
	svc := newMockNotesService()
	ctx := context.Background()

	tests := []struct {
		name    string
		req     *pb.CreateNoteRequest
		wantErr codes.Code
	}{
		{
			name: "valid note",
			req: &pb.CreateNoteRequest{
				UserId:  "user-123",
				Content: "Test note content",
				Tags:    []string{"tag1", "tag2"},
			},
			wantErr: codes.OK,
		},
		{
			name: "missing user_id",
			req: &pb.CreateNoteRequest{
				Content: "Test note content",
			},
			wantErr: codes.InvalidArgument,
		},
		{
			name: "missing content",
			req: &pb.CreateNoteRequest{
				UserId: "user-123",
			},
			wantErr: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.CreateNote(ctx, tt.req)

			if tt.wantErr == codes.OK {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if resp == nil || resp.Note == nil {
					t.Error("expected note in response")
				}
				if resp.Note.Content != tt.req.Content {
					t.Errorf("expected content %q, got %q", tt.req.Content, resp.Note.Content)
				}
			} else {
				if err == nil {
					t.Error("expected error, got nil")
				}
				st, ok := status.FromError(err)
				if !ok {
					t.Errorf("expected gRPC status error, got %v", err)
				}
				if st.Code() != tt.wantErr {
					t.Errorf("expected error code %v, got %v", tt.wantErr, st.Code())
				}
			}
		})
	}
}

func TestGetNote(t *testing.T) {
	svc := newMockNotesService()
	ctx := context.Background()

	// Create a note first
	createResp, _ := svc.CreateNote(ctx, &pb.CreateNoteRequest{
		UserId:  "user-123",
		Content: "Test content",
	})

	tests := []struct {
		name    string
		req     *pb.GetNoteRequest
		wantErr codes.Code
	}{
		{
			name: "valid get",
			req: &pb.GetNoteRequest{
				UserId: "user-123",
				Id:     createResp.Note.Id,
			},
			wantErr: codes.OK,
		},
		{
			name: "missing user_id",
			req: &pb.GetNoteRequest{
				Id: createResp.Note.Id,
			},
			wantErr: codes.InvalidArgument,
		},
		{
			name: "missing id",
			req: &pb.GetNoteRequest{
				UserId: "user-123",
			},
			wantErr: codes.InvalidArgument,
		},
		{
			name: "wrong user",
			req: &pb.GetNoteRequest{
				UserId: "user-456",
				Id:     createResp.Note.Id,
			},
			wantErr: codes.NotFound,
		},
		{
			name: "non-existent note",
			req: &pb.GetNoteRequest{
				UserId: "user-123",
				Id:     "non-existent",
			},
			wantErr: codes.NotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.GetNote(ctx, tt.req)

			if tt.wantErr == codes.OK {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if resp == nil || resp.Note == nil {
					t.Error("expected note in response")
				}
			} else {
				if err == nil {
					t.Error("expected error, got nil")
				}
				st, ok := status.FromError(err)
				if !ok {
					t.Errorf("expected gRPC status error, got %v", err)
				}
				if st.Code() != tt.wantErr {
					t.Errorf("expected error code %v, got %v", tt.wantErr, st.Code())
				}
			}
		})
	}
}

func TestListNotes(t *testing.T) {
	svc := newMockNotesService()
	ctx := context.Background()

	// Create some notes
	_, _ = svc.CreateNote(ctx, &pb.CreateNoteRequest{
		UserId:  "user-123",
		Content: "Note 1",
	})
	_, _ = svc.CreateNote(ctx, &pb.CreateNoteRequest{
		UserId:  "user-123",
		Content: "Note 2",
	})

	tests := []struct {
		name    string
		req     *pb.ListNotesRequest
		wantErr codes.Code
	}{
		{
			name: "valid list",
			req: &pb.ListNotesRequest{
				UserId: "user-123",
			},
			wantErr: codes.OK,
		},
		{
			name: "missing user_id",
			req: &pb.ListNotesRequest{
				Limit: 10,
			},
			wantErr: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.ListNotes(ctx, tt.req)

			if tt.wantErr == codes.OK {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if resp == nil {
					t.Error("expected response")
				}
			} else {
				if err == nil {
					t.Error("expected error, got nil")
				}
				st, ok := status.FromError(err)
				if !ok {
					t.Errorf("expected gRPC status error, got %v", err)
				}
				if st.Code() != tt.wantErr {
					t.Errorf("expected error code %v, got %v", tt.wantErr, st.Code())
				}
			}
		})
	}
}

func TestDeleteNote(t *testing.T) {
	svc := newMockNotesService()
	ctx := context.Background()

	// Create a note first
	createResp, _ := svc.CreateNote(ctx, &pb.CreateNoteRequest{
		UserId:  "user-123",
		Content: "Test content",
	})

	tests := []struct {
		name        string
		req         *pb.DeleteNoteRequest
		wantErr     codes.Code
		wantSuccess bool
	}{
		{
			name: "valid delete",
			req: &pb.DeleteNoteRequest{
				UserId: "user-123",
				Id:     createResp.Note.Id,
			},
			wantErr:     codes.OK,
			wantSuccess: true,
		},
		{
			name: "missing user_id",
			req: &pb.DeleteNoteRequest{
				Id: createResp.Note.Id,
			},
			wantErr: codes.InvalidArgument,
		},
		{
			name: "missing id",
			req: &pb.DeleteNoteRequest{
				UserId: "user-123",
			},
			wantErr: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.DeleteNote(ctx, tt.req)

			if tt.wantErr == codes.OK {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if resp.Success != tt.wantSuccess {
					t.Errorf("expected success=%v, got %v", tt.wantSuccess, resp.Success)
				}
			} else {
				if err == nil {
					t.Error("expected error, got nil")
				}
				st, ok := status.FromError(err)
				if !ok {
					t.Errorf("expected gRPC status error, got %v", err)
				}
				if st.Code() != tt.wantErr {
					t.Errorf("expected error code %v, got %v", tt.wantErr, st.Code())
				}
			}
		})
	}
}
