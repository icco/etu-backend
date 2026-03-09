package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/icco/etu-backend/internal/db"
	"github.com/icco/etu-backend/internal/models"
	"github.com/icco/etu-backend/internal/storage"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UserSettingsService implements the UserSettings gRPC service
type UserSettingsService struct {
	pb.UnimplementedUserSettingsServiceServer
	db          *db.DB
	storage     *storage.Client
	imgixDomain string
	log         *slog.Logger
}

// NewUserSettingsService creates a new UserSettingsService
func NewUserSettingsService(database *db.DB, storageClient *storage.Client, imgixDomain string) *UserSettingsService {
	return &UserSettingsService{
		db:          database,
		storage:     storageClient,
		imgixDomain: imgixDomain,
		log:         slog.Default().With("service", "user_settings"),
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

	// Refresh signed URL if profile image is stored in GCS
	s.refreshProfileImageURL(ctx, user)

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

	var image *string
	var profileImageGCSObject *string

	// Handle profile image upload
	if req.ProfileImageUpload != nil {
		if s.storage == nil {
			return nil, status.Error(codes.FailedPrecondition, "storage client not configured")
		}

		if err := validateImage(req.ProfileImageUpload.Data, req.ProfileImageUpload.MimeType); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid profile image: %v", err)
		}

		// Delete old GCS object if exists
		existingUser, err := s.db.GetUserSettings(ctx, req.UserId)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to get user: %v", err)
		}
		if existingUser != nil && existingUser.ProfileImageGCSObject != nil && *existingUser.ProfileImageGCSObject != "" {
			if delErr := s.storage.DeleteImage(ctx, *existingUser.ProfileImageGCSObject); delErr != nil {
				s.log.Warn("failed to delete old profile image", "error", delErr, "object", *existingUser.ProfileImageGCSObject)
			}
		}

		// Upload new image
		objectName := fmt.Sprintf("profiles/%s/avatar", req.UserId)
		url, err := s.storage.UploadImage(ctx, objectName, req.ProfileImageUpload.Data, req.ProfileImageUpload.MimeType)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to upload profile image: %v", err)
		}

		image = &url
		profileImageGCSObject = &objectName
	} else if req.Image != nil {
		image = req.Image
		// Clear GCS object if user is setting a URL directly
		empty := ""
		profileImageGCSObject = &empty
	}

	user, err := s.db.UpdateUserSettings(ctx, req.UserId, req.NotionKey, req.Name, image, req.Password, req.NotionDatabaseName, profileImageGCSObject)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update user settings: %v", err)
	}

	// Refresh signed URL for response
	s.refreshProfileImageURL(ctx, user)

	return &pb.UpdateUserSettingsResponse{
		User: userToProto(user),
	}, nil
}

// refreshProfileImageURL re-signs the profile image URL if stored in GCS
func (s *UserSettingsService) refreshProfileImageURL(ctx context.Context, user *models.User) {
	if user.ProfileImageGCSObject == nil || *user.ProfileImageGCSObject == "" {
		return
	}
	if s.imgixDomain != "" {
		url := fmt.Sprintf("https://%s/%s", s.imgixDomain, *user.ProfileImageGCSObject)
		user.Image = &url
		return
	}
	if s.storage != nil {
		url, err := s.storage.GetSignedURL(ctx, *user.ProfileImageGCSObject)
		if err != nil {
			s.log.Warn("failed to refresh profile image signed URL", "error", err)
			return
		}
		user.Image = &url
	}
}
