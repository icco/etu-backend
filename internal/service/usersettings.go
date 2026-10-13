package service

import (
	"context"
	"fmt"
	"time"

	"go.icco.me/etu-backend/internal/db"
	"go.icco.me/etu-backend/internal/storage"
	pb "go.icco.me/etu-backend/proto"
	"go.icco.me/gutil/logging"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
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

	if req.Password != nil && *req.Password != "" {
		if err := s.changePassword(ctx, req.UserId, req.GetCurrentPassword(), *req.Password); err != nil {
			return nil, err
		}
	}

	l := logging.FromContext(ctx)

	var profileImageGCSObject *string

	if req.ProfileImageUpload != nil {
		if s.storage == nil {
			return nil, status.Error(codes.FailedPrecondition, "storage client not configured")
		}

		if err := validateImage(req.ProfileImageUpload.Data, req.ProfileImageUpload.MimeType); err != nil {
			l.Errorw("profile image validation failed", "user_id", req.UserId, zap.Error(err))
			return nil, status.Errorf(codes.InvalidArgument, "invalid profile image: %v", err)
		}

		// Capture any prior avatar object so we can delete it after a successful overwrite.
		// Using a unique per-upload path avoids imgix's origin cache serving stale bytes
		// from a fixed key, which it does even when the imgix request URL has a fresh
		// cache-busting query param (imgix origin cache keys ignore query strings).
		var oldObjectName string
		if existing, getErr := s.db.GetUser(ctx, req.UserId); getErr == nil && existing != nil && existing.ProfileImageGCSObject != nil {
			oldObjectName = *existing.ProfileImageGCSObject
		}

		objectName := fmt.Sprintf("profiles/%s/avatar-%d", req.UserId, time.Now().UnixNano())
		if _, err := s.storage.UploadImage(ctx, objectName, req.ProfileImageUpload.Data, req.ProfileImageUpload.MimeType); err != nil {
			l.Errorw("GCS upload failed", "user_id", req.UserId, "object_name", objectName, zap.Error(err))
			return nil, status.Errorf(codes.Internal, "failed to upload profile image: %v", err)
		}

		l.Infow("GCS upload succeeded", "user_id", req.UserId, "object_name", objectName, "bytes", len(req.ProfileImageUpload.Data))

		profileImageGCSObject = &objectName

		if oldObjectName != "" && oldObjectName != objectName {
			if err := s.storage.DeleteImage(ctx, oldObjectName); err != nil {
				l.Warnw("failed to delete previous avatar (orphaned object left in GCS)",
					"user_id", req.UserId, "old_object_name", oldObjectName, zap.Error(err))
			}
		}
	} else if req.ClearProfileImage != nil && *req.ClearProfileImage {
		empty := ""
		profileImageGCSObject = &empty
	}

	user, err := s.db.UpdateUserSettings(ctx, req.UserId, req.NotionKey, req.Name, nil, req.NotionDatabaseName, profileImageGCSObject)
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
		signed := mediaURL(s.imgixDomain, req.Key, time.Now())
		if signed == "" {
			return nil, status.Error(codes.FailedPrecondition, "media signing not configured")
		}
		return &pb.GetProfileImageURLResponse{
			Url: signed,
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

// changePassword revokes every API key and mobile session, including the
// caller's, so a leaked credential cannot survive a password change.
func (s *UserSettingsService) changePassword(ctx context.Context, userID, current, next string) error {
	if current == "" {
		return status.Error(codes.InvalidArgument, "current_password is required to change password")
	}
	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get user: %v", err)
	}
	if user == nil {
		return status.Error(codes.NotFound, "user not found")
	}
	if user.Disabled {
		return status.Error(codes.PermissionDenied, "account is disabled")
	}
	if db.LockoutRemaining(user.FailedLoginAttempts, user.LastFailedLogin, time.Now()) > 0 {
		return status.Error(codes.PermissionDenied, "account is locked")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(current)); err != nil {
		if recordErr := s.db.RecordFailedLogin(ctx, userID); recordErr != nil {
			return status.Errorf(codes.Internal, "failed to record failed attempt: %v", recordErr)
		}
		return status.Error(codes.PermissionDenied, "current password is incorrect")
	}
	found, err := s.db.ChangePassword(ctx, userID, next)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to change password: %v", err)
	}
	if !found {
		return status.Error(codes.NotFound, "user not found")
	}
	return nil
}
