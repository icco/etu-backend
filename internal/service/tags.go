package service

import (
	"context"

	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TagsService implements the TagsService gRPC service
type TagsService struct {
	pb.UnimplementedTagsServiceServer
	db *db.DB
}

// NewTagsService creates a new TagsService
func NewTagsService(database *db.DB) *TagsService {
	return &TagsService{db: database}
}

// ListTags retrieves all tags for a user with usage counts
func (s *TagsService) ListTags(ctx context.Context, req *pb.ListTagsRequest) (*pb.ListTagsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Verify authorization
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	tags, err := s.db.ListTags(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list tags: %v", err)
	}

	pbTags := make([]*pb.Tag, len(tags))
	for i, t := range tags {
		pbTags[i] = &pb.Tag{
			Id:        t.ID,
			Name:      t.Name,
			Count:     int32(t.Count),
			CreatedAt: &pb.Timestamp{Seconds: t.CreatedAt.Unix(), Nanos: int32(t.CreatedAt.Nanosecond())},
		}
	}

	return &pb.ListTagsResponse{
		Tags: pbTags,
	}, nil
}
