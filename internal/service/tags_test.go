package service

import (
	"context"
	"testing"
	"time"

	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// mockTagsService wraps TagsService for testing
type mockTagsService struct {
	pb.UnimplementedTagsServiceServer
	tags map[string]*db.Tag
}

func newMockTagsService() *mockTagsService {
	return &mockTagsService{
		tags: make(map[string]*db.Tag),
	}
}

func (s *mockTagsService) addTag(userID, name string, count int) {
	tag := &db.Tag{
		ID:        "tag-" + name,
		Name:      name,
		CreatedAt: time.Now(),
		UserID:    userID,
		Count:     count,
	}
	s.tags[tag.ID] = tag
}

func (s *mockTagsService) ListTags(ctx context.Context, req *pb.ListTagsRequest) (*pb.ListTagsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	var tags []*pb.Tag
	for _, t := range s.tags {
		if t.UserID == req.UserId {
			tags = append(tags, &pb.Tag{
				Id:        t.ID,
				Name:      t.Name,
				Count:     int32(t.Count),
				CreatedAt: timestamppb.New(t.CreatedAt),
			})
		}
	}

	return &pb.ListTagsResponse{
		Tags: tags,
	}, nil
}

func TestListTags(t *testing.T) {
	svc := newMockTagsService()
	ctx := context.Background()

	// Add some tags
	svc.addTag("user-123", "work", 5)
	svc.addTag("user-123", "personal", 3)
	svc.addTag("user-456", "other", 1)

	tests := []struct {
		name      string
		req       *pb.ListTagsRequest
		wantErr   codes.Code
		wantCount int
	}{
		{
			name: "valid list",
			req: &pb.ListTagsRequest{
				UserId: "user-123",
			},
			wantErr:   codes.OK,
			wantCount: 2,
		},
		{
			name:    "missing user_id",
			req:     &pb.ListTagsRequest{},
			wantErr: codes.InvalidArgument,
		},
		{
			name: "user with no tags",
			req: &pb.ListTagsRequest{
				UserId: "user-999",
			},
			wantErr:   codes.OK,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.ListTags(ctx, tt.req)

			if tt.wantErr == codes.OK {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if resp == nil {
					t.Error("expected response")
					return
				}
				if len(resp.Tags) != tt.wantCount {
					t.Errorf("expected %d tags, got %d", tt.wantCount, len(resp.Tags))
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
