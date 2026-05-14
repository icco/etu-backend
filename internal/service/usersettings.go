package service

import (
	"context"
	"fmt"

	"github.com/icco/etu-backend/internal/db"
	"github.com/icco/etu-backend/internal/storage"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UserSettingsService implements the UserSettings gRPC service.
type UserSettingsService struct {
	pb.UnimplementedUserSettingsServiceServer
	db          *db.DB
	storage     *storage.Client
	imgixDomain string
}

// NewUserSettingsService creates a new UserSettingsService
func NewUserSettingsService(database *db.DB, storageClient *storage.Client, imgixDomain string) *UserSettingsService {
	return &UserSettingsService{
		db:          database,
		storage:     storageClient,
		imgixDomain: imgixDomain,
	}
}

// GetUserSettings retrieves user settings
func (s *UserSettingsService) GetUserSettings(ctx context.Context, req *pb.GetUserSettingsRequest) (*pb.GetUserSettingsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

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

	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	var profileImageGCSObject *string

	if req.ProfileImageUpload != nil {
		if s.storage == nil {
			return nil, status.Error(codes.FailedPrecondition, "storage client not configured")
		}

		if err := validateImage(req.ProfileImageUpload.Data, req.ProfileImageUpload.MimeType); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid profile image: %v", err)
		}

		// Fixed path overwrites any existing avatar for this user.
		objectName := fmt.Sprintf("profiles/%s/avatar", req.UserId)
		if _, err := s.storage.UploadImage(ctx, objectName, req.ProfileImageUpload.Data, req.ProfileImageUpload.MimeType); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to upload profile image: %v", err)
		}

		profileImageGCSObject = &objectName
	} else if req.ClearProfileImage != nil && *req.ClearProfileImage {
		empty := ""
		profileImageGCSObject = &empty
	}

	user, err := s.db.UpdateUserSettings(ctx, req.UserId, req.NotionKey, req.Name, req.Password, req.NotionDatabaseName, profileImageGCSObject)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update user settings: %v", err)
	}

	return &pb.UpdateUserSettingsResponse{
		User: userToProto(user),
	}, nil
}

// GetProfileImageURL resolves a stored avatar key to a short-lived URL. The
// key is whatever was returned in User.image — typically "profiles/{userId}/avatar".
func (s *UserSettingsService) GetProfileImageURL(ctx context.Context, req *pb.GetProfileImageURLRequest) (*pb.GetProfileImageURLResponse, error) {
	if req.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}

	if s.imgixDomain != "" {
		return &pb.GetProfileImageURLResponse{
			Url: fmt.Sprintf("https://%s/%s", s.imgixDomain, req.Key),
		}, nil
	}

	if s.storage == nil {
		return nil, status.Error(codes.FailedPrecondition, "storage client not configured")
	}

	url, err := s.storage.GetSignedURL(ctx, req.Key)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to sign profile image URL: %v", err)
	}

	return &pb.GetProfileImageURLResponse{Url: url}, nil
}
