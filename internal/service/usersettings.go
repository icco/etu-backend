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

	// Verify the authenticated user is authorized to access this user's settings
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	user, err := s.db.GetUserSettings(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get user settings: %v", err)
	}

	if user == nil {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	return &pb.GetUserSettingsResponse{
		User: userToProto(user),
	}, nil
}

// UpdateUserSettings updates user settings
func (s *UserSettingsService) UpdateUserSettings(ctx context.Context, req *pb.UpdateUserSettingsRequest) (*pb.UpdateUserSettingsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Verify the authenticated user is authorized to update this user's settings
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	user, err := s.db.UpdateUserSettings(ctx, req.UserId, req.NotionKey, req.Name, req.Image, req.Password, req.NotionDatabaseName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update user settings: %v", err)
	}

	return &pb.UpdateUserSettingsResponse{
		User: userToProto(user),
	}, nil
}
