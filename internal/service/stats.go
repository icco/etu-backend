package service

import (
	"context"

	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/db"
	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StatsService implements the StatsService gRPC service
type StatsService struct {
	pb.UnimplementedStatsServiceServer
	db *db.DB
}

// NewStatsService creates a new StatsService
func NewStatsService(database *db.DB) *StatsService {
	return &StatsService{
		db: database,
	}
}

// GetStats retrieves statistics for a user, or for all users when an M2M
// caller omits user_id. API key callers are always scoped to themselves.
func (s *StatsService) GetStats(ctx context.Context, req *pb.GetStatsRequest) (*pb.GetStatsResponse, error) {
	userID := req.UserId
	if userID != "" {
		if err := verifyUserAuthorization(ctx, userID); err != nil {
			return nil, err
		}
	} else if !auth.IsM2MAuth(ctx) {
		authenticatedUserID, err := auth.GetUserID(ctx)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "authentication required")
		}
		userID = authenticatedUserID
	}

	totalBlips, uniqueTags, wordsWritten, err := s.db.GetStats(ctx, userID)

	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get stats: %v", err)
	}

	return &pb.GetStatsResponse{
		TotalBlips:   totalBlips,
		UniqueTags:   uniqueTags,
		WordsWritten: wordsWritten,
	}, nil
}
