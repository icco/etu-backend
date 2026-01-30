package service

import (
	"context"

	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UserSettingsService implements the UserSettings gRPC service
type UserSettingsService struct {
	pb.UnimplementedUserSettingsServiceServer
	db *db.DB
}

// NewUserSettingsService creates a new UserSettingsService
func NewUserSettingsService(database *db.DB) *UserSettingsService {
	return &UserSettingsService{
		db: database,
	}
}

// GetUserSettings retrieves user settings
func (s *UserSettingsService) GetUserSettings(ctx context.Context, req *pb.GetUserSettingsRequest) (*pb.GetUserSettingsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// TODO: Verify that authenticated user matches req.UserId
	// This should be done via middleware or by extracting user ID from context
	// For now, assuming authentication is handled by the gRPC interceptor

	user, err := s.db.GetUserSettings(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get user settings: %v", err)
	}
	
	if user == nil {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	return &pb.GetUserSettingsResponse{
		Settings: &pb.UserSettings{
			UserId:    user.ID,
			NotionKey: user.NotionKey,
			Username:  user.Username,
			CreatedAt: &pb.Timestamp{
				Seconds: user.CreatedAt.Unix(),
				Nanos:   int32(user.CreatedAt.Nanosecond()),
			},
			UpdatedAt: &pb.Timestamp{
				Seconds: user.UpdatedAt.Unix(),
				Nanos:   int32(user.UpdatedAt.Nanosecond()),
			},
		},
	}, nil
}

// UpdateUserSettings updates user settings
func (s *UserSettingsService) UpdateUserSettings(ctx context.Context, req *pb.UpdateUserSettingsRequest) (*pb.UpdateUserSettingsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// TODO: Verify that authenticated user matches req.UserId
	// This should be done via middleware or by extracting user ID from context
	// For now, assuming authentication is handled by the gRPC interceptor

	user, err := s.db.UpdateUserSettings(ctx, req.UserId, req.NotionKey, req.Username)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update user settings: %v", err)
	}

	return &pb.UpdateUserSettingsResponse{
		Settings: &pb.UserSettings{
			UserId:    user.ID,
			NotionKey: user.NotionKey,
			Username:  user.Username,
			CreatedAt: &pb.Timestamp{
				Seconds: user.CreatedAt.Unix(),
				Nanos:   int32(user.CreatedAt.Nanosecond()),
			},
			UpdatedAt: &pb.Timestamp{
				Seconds: user.UpdatedAt.Unix(),
				Nanos:   int32(user.UpdatedAt.Nanosecond()),
			},
		},
	}, nil
}
