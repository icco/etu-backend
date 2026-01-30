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

	settings, err := s.db.GetUserSettings(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get user settings: %v", err)
	}

	return &pb.GetUserSettingsResponse{
		Settings: &pb.UserSettings{
			UserId:    settings.UserID,
			NotionKey: settings.NotionKey,
			Username:  settings.Username,
			CreatedAt: &pb.Timestamp{
				Seconds: settings.CreatedAt.Unix(),
				Nanos:   int32(settings.CreatedAt.Nanosecond()),
			},
			UpdatedAt: &pb.Timestamp{
				Seconds: settings.UpdatedAt.Unix(),
				Nanos:   int32(settings.UpdatedAt.Nanosecond()),
			},
		},
	}, nil
}

// UpdateUserSettings updates user settings
func (s *UserSettingsService) UpdateUserSettings(ctx context.Context, req *pb.UpdateUserSettingsRequest) (*pb.UpdateUserSettingsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Extract optional fields
	var notionKey, username *string
	if req.NotionKey != nil {
		notionKey = req.NotionKey
	}
	if req.Username != nil {
		username = req.Username
	}

	settings, err := s.db.UpdateUserSettings(ctx, req.UserId, notionKey, username)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update user settings: %v", err)
	}

	return &pb.UpdateUserSettingsResponse{
		Settings: &pb.UserSettings{
			UserId:    settings.UserID,
			NotionKey: settings.NotionKey,
			Username:  settings.Username,
			CreatedAt: &pb.Timestamp{
				Seconds: settings.CreatedAt.Unix(),
				Nanos:   int32(settings.CreatedAt.Nanosecond()),
			},
			UpdatedAt: &pb.Timestamp{
				Seconds: settings.UpdatedAt.Unix(),
				Nanos:   int32(settings.UpdatedAt.Nanosecond()),
			},
		},
	}, nil
}
