package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/db"
	"go.icco.me/etu-backend/internal/storage"
	pb "go.icco.me/etu-backend/proto"
	"go.icco.me/gutil/logging"
	"go.uber.org/zap"
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

	l := logging.FromContext(ctx)

	if req.ProfileImageUpload != nil {
		if s.storage == nil {
			return nil, status.Error(codes.FailedPrecondition, "storage client not configured")
		}

		if err := validateImage(req.ProfileImageUpload.Data, req.ProfileImageUpload.MimeType); err != nil {
			l.Errorw("profile image validation failed", "user_id", req.UserId, zap.Error(err))
			return nil, status.Errorf(codes.InvalidArgument, "invalid profile image: %v", err)
		}
	}

	changingPassword := req.Password != nil && *req.Password != ""
	if changingPassword && req.GetCurrentPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "current_password is required to change password")
	}

	var profileImageGCSObject *string
	var oldObjectName, newObjectName string
	if req.ProfileImageUpload != nil {
		// Capture any prior avatar object so we can delete it after a successful overwrite.
		// Using a unique per-upload path avoids imgix's origin cache serving stale bytes
		// from a fixed key, which it does even when the imgix request URL has a fresh
		// cache-busting query param (imgix origin cache keys ignore query strings).
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
		newObjectName = objectName
	} else if req.ClearProfileImage != nil && *req.ClearProfileImage {
		empty := ""
		profileImageGCSObject = &empty
	}

	var user *db.User
	var err error
	if changingPassword {
		user, err = s.db.ChangePasswordWithSettingsAndAI(ctx, req.UserId, req.GetCurrentPassword(), *req.Password, req.NotionKey, req.Name, req.NotionDatabaseName, profileImageGCSObject, req.AiEnabled)
	} else {
		user, err = s.db.UpdateUserSettingsWithAI(ctx, req.UserId, req.NotionKey, req.Name, nil, req.NotionDatabaseName, profileImageGCSObject, req.AiEnabled)
	}
	if err != nil {
		if newObjectName != "" {
			if delErr := s.storage.DeleteImage(ctx, newObjectName); delErr != nil {
				l.Errorw("failed to delete uploaded avatar after settings update failed", "user_id", req.UserId, "object_name", newObjectName, zap.Error(delErr))
			}
		}
		return nil, settingsError(err)
	}

	if oldObjectName != "" && oldObjectName != newObjectName && newObjectName != "" {
		if err := s.storage.DeleteImage(ctx, oldObjectName); err != nil {
			l.Warnw("failed to delete previous avatar (orphaned object left in GCS)",
				"user_id", req.UserId, "old_object_name", oldObjectName, zap.Error(err))
		}
	}

	return &pb.UpdateUserSettingsResponse{
		User: userToProto(user),
	}, nil
}

// GetProfileImageURL resolves the caller's stored avatar to a short-lived URL.
// The signed key always comes from the user record; a request key is only
// accepted when it matches that record.
func (s *UserSettingsService) GetProfileImageURL(ctx context.Context, req *pb.GetProfileImageURLRequest) (*pb.GetProfileImageURLResponse, error) {
	userID, err := profileImageOwner(ctx, req)
	if err != nil {
		return nil, err
	}

	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get user: %v", err)
	}
	if user == nil || user.ProfileImageGCSObject == nil || *user.ProfileImageGCSObject == "" {
		return nil, status.Error(codes.NotFound, "profile image not found")
	}
	key := *user.ProfileImageGCSObject
	if req.Key != "" && req.Key != key {
		return nil, status.Error(codes.PermissionDenied, "key does not match profile image")
	}

	if s.imgixDomain != "" {
		signed := mediaURL(s.imgixDomain, key, time.Now())
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

	url, err := s.storage.GetSignedURL(ctx, key)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to sign profile image URL: %v", err)
	}

	return &pb.GetProfileImageURLResponse{Url: url}, nil
}

// settingsError maps settings and password-change failures to gRPC statuses.
func settingsError(err error) error {
	switch {
	case errors.Is(err, db.ErrUserNotFound):
		return status.Error(codes.NotFound, "user not found")
	case errors.Is(err, db.ErrAccountDisabled), errors.Is(err, db.ErrAccountLocked), errors.Is(err, db.ErrWrongPassword):
		return status.Error(codes.PermissionDenied, err.Error())
	default:
		return status.Errorf(codes.Internal, "failed to update user settings: %v", err)
	}
}

// profileImageOwner picks whose avatar to resolve. M2M callers without a
// user_id fall back to the owner segment of a "profiles/{userId}/..." key.
func profileImageOwner(ctx context.Context, req *pb.GetProfileImageURLRequest) (string, error) {
	if !auth.IsM2MAuth(ctx) {
		userID, err := auth.GetUserID(ctx)
		if err != nil {
			return "", status.Error(codes.Unauthenticated, "authentication required")
		}
		if req.UserId != "" && req.UserId != userID {
			return "", status.Error(codes.PermissionDenied, "cannot access another user's data")
		}
		return userID, nil
	}
	if req.UserId != "" {
		return req.UserId, nil
	}
	owner, ok := ProfileImageKeyOwner(req.Key)
	if !ok {
		return "", status.Error(codes.InvalidArgument, "user_id or a profile image key is required")
	}
	return owner, nil
}

// ProfileImageKeyOwner returns the user ID embedded in a profiles/{userId}/ storage key.
func ProfileImageKeyOwner(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, "profiles/")
	owner, _, found := strings.Cut(rest, "/")
	if !ok || !found || owner == "" {
		return "", false
	}
	return owner, true
}
