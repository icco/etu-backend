package service

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/icco/etu-backend/internal/db"
	"github.com/icco/etu-backend/internal/models"
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

// mockNoteToProto converts a db.Note to a protobuf Note for testing
func mockNoteToProto(n *db.Note) *pb.Note {
	tagNames := make([]string, len(n.Tags))
	for i, t := range n.Tags {
		tagNames[i] = t.Name
	}

	pbImages := make([]*pb.NoteImage, len(n.Images))
	for i, img := range n.Images {
		pbImages[i] = &pb.NoteImage{
			Id:            img.ID,
			Url:           img.URL,
			ExtractedText: img.ExtractedText,
			MimeType:      img.MimeType,
		}
	}

	return &pb.Note{
		Id:      n.ID,
		Content: n.Content,
		Tags:    tagNames,
		Images:  pbImages,
	}
}

func (s *mockNotesService) CreateNote(ctx context.Context, req *pb.CreateNoteRequest) (*pb.CreateNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Content == "" && len(req.Images) == 0 {
		return nil, status.Error(codes.InvalidArgument, "content or images is required")
	}
	// Mock: if images provided but no content, simulate storage not configured
	if req.Content == "" && len(req.Images) > 0 {
		return nil, status.Error(codes.FailedPrecondition, "image storage is not configured")
	}

	now := time.Now()

	// Convert []string to []Tag
	tags := make([]models.Tag, len(req.Tags))
	for i, name := range req.Tags {
		tags[i] = models.Tag{Name: name}
	}

	note := &db.Note{
		ID:        models.GenerateCUID(),
		Content:   req.Content,
		CreatedAt: now,
		UpdatedAt: now,
		UserID:    req.UserId,
		Tags:      tags,
	}
	s.notes[note.ID] = note

	return &pb.CreateNoteResponse{
		Note: mockNoteToProto(note),
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
		Note: mockNoteToProto(note),
	}, nil
}

func (s *mockNotesService) ListNotes(ctx context.Context, req *pb.ListNotesRequest) (*pb.ListNotesResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	var notes []*pb.Note
	for _, n := range s.notes {
		if n.UserID == req.UserId {
			notes = append(notes, mockNoteToProto(n))
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

func (s *mockNotesService) GetRandomNotes(ctx context.Context, req *pb.GetRandomNotesRequest) (*pb.GetRandomNotesResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Collect all notes for the user
	var userNotes []*db.Note
	for _, n := range s.notes {
		if n.UserID == req.UserId {
			userNotes = append(userNotes, n)
		}
	}

	// Return up to count notes (or all if less than count), in random order
	count := int(req.Count)
	if count <= 0 {
		count = 5
	}
	if count > len(userNotes) {
		count = len(userNotes)
	}

	// Shuffle so we don't always return map/insertion order
	shuffled := make([]*db.Note, len(userNotes))
	copy(shuffled, userNotes)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	notes := make([]*pb.Note, count)
	for i := 0; i < count; i++ {
		notes[i] = mockNoteToProto(shuffled[i])
	}

	return &pb.GetRandomNotesResponse{
		Notes: notes,
	}, nil
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
			name: "missing content and images",
			req: &pb.CreateNoteRequest{
				UserId: "user-123",
			},
			wantErr: codes.InvalidArgument,
		},
		{
			name: "image only without storage configured",
			req: &pb.CreateNoteRequest{
				UserId: "user-123",
				Images: []*pb.ImageUpload{{Data: []byte("test"), MimeType: "image/png"}},
			},
			wantErr: codes.FailedPrecondition,
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

func TestGetRandomNotes(t *testing.T) {
	svc := newMockNotesService()
	ctx := context.Background()

	// Create several notes for testing
	for i := 1; i <= 10; i++ {
		_, _ = svc.CreateNote(ctx, &pb.CreateNoteRequest{
			UserId:  "user-123",
			Content: fmt.Sprintf("Test note %d", i),
		})
	}

	// Create a smaller set of notes for another user
	for i := 1; i <= 2; i++ {
		_, _ = svc.CreateNote(ctx, &pb.CreateNoteRequest{
			UserId:  "user-456",
			Content: fmt.Sprintf("Test note for user-456: %d", i),
		})
	}

	tests := []struct {
		name         string
		req          *pb.GetRandomNotesRequest
		wantErr      codes.Code
		wantNotesMin int
		wantNotesMax int
	}{
		{
			name: "valid get random notes with default count",
			req: &pb.GetRandomNotesRequest{
				UserId: "user-123",
			},
			wantErr:      codes.OK,
			wantNotesMin: 5,
			wantNotesMax: 5,
		},
		{
			name: "valid get random notes with custom count",
			req: &pb.GetRandomNotesRequest{
				UserId: "user-123",
				Count:  3,
			},
			wantErr:      codes.OK,
			wantNotesMin: 3,
			wantNotesMax: 3,
		},
		{
			name: "valid get random notes with count larger than available",
			req: &pb.GetRandomNotesRequest{
				UserId: "user-123",
				Count:  20,
			},
			wantErr:      codes.OK,
			wantNotesMin: 10,
			wantNotesMax: 10,
		},
		{
			name: "request more notes than available (small dataset)",
			req: &pb.GetRandomNotesRequest{
				UserId: "user-456",
				Count:  5,
			},
			wantErr:      codes.OK,
			wantNotesMin: 2,
			wantNotesMax: 2,
		},
		{
			name: "missing user_id",
			req: &pb.GetRandomNotesRequest{
				Count: 5,
			},
			wantErr: codes.InvalidArgument,
		},
		{
			name: "user with no notes",
			req: &pb.GetRandomNotesRequest{
				UserId: "user-no-notes",
				Count:  5,
			},
			wantErr:      codes.OK,
			wantNotesMin: 0,
			wantNotesMax: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.GetRandomNotes(ctx, tt.req)

			if tt.wantErr == codes.OK {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if resp == nil {
					t.Error("expected response")
				} else {
					notesCount := len(resp.Notes)
					if notesCount < tt.wantNotesMin || notesCount > tt.wantNotesMax {
						t.Errorf("expected %d-%d notes, got %d", tt.wantNotesMin, tt.wantNotesMax, notesCount)
					}
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

	t.Run("returns notes in random order across calls", func(t *testing.T) {
		// Use a fresh service with enough notes that same order repeatedly is very unlikely
		svc := newMockNotesService()
		for i := 1; i <= 10; i++ {
			_, _ = svc.CreateNote(ctx, &pb.CreateNoteRequest{
				UserId:  "user-123",
				Content: fmt.Sprintf("Note %d", i),
			})
		}

		// Collect orderings from multiple calls (by note IDs)
		orderings := make([][]string, 0, 20)
		for i := 0; i < 20; i++ {
			resp, err := svc.GetRandomNotes(ctx, &pb.GetRandomNotesRequest{
				UserId: "user-123",
				Count:  5,
			})
			if err != nil {
				t.Fatalf("GetRandomNotes: %v", err)
			}
			if len(resp.Notes) != 5 {
				t.Fatalf("expected 5 notes, got %d", len(resp.Notes))
			}
			ids := make([]string, len(resp.Notes))
			for j, n := range resp.Notes {
				ids[j] = n.Id
			}
			orderings = append(orderings, ids)
		}

		// At least two responses must differ in order (proves we're not always returning same order)
		var seenDifferent bool
		for i := 0; i < len(orderings); i++ {
			for j := i + 1; j < len(orderings); j++ {
				if !sliceEqual(orderings[i], orderings[j]) {
					seenDifferent = true
					break
				}
			}
			if seenDifferent {
				break
			}
		}
		if !seenDifferent {
			t.Error("GetRandomNotes returned the same order 20 times in a row; expected random order to vary across calls")
		}
	})
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
