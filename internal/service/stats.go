package service

import (
	"context"

	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
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

// GetStats retrieves statistics for a user or all users
func (s *StatsService) GetStats(ctx context.Context, req *pb.GetStatsRequest) (*pb.GetStatsResponse, error) {
	// If user_id is provided, verify authorization
	if req.UserId != "" {
		if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
			return nil, err
		}
	}
	// If user_id is empty, we get stats for all users
	// This could be restricted based on authorization if needed

	totalBlips, uniqueTags, wordsWritten, err := s.db.GetStats(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get stats: %v", err)
	}

	return &pb.GetStatsResponse{
		TotalBlips:   totalBlips,
		UniqueTags:   uniqueTags,
		WordsWritten: wordsWritten,
	}, nil
}
