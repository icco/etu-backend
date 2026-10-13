package service

import (
	"context"
	"fmt"
	"time"

	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/db"
	"go.icco.me/etu-backend/internal/storage"
	pb "go.icco.me/etu-backend/proto"
	"go.icco.me/gutil/logging"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	exportPageSize  = 200
	exportURLExpiry = time.Hour
)

// SubscriptionCanceler cancels a customer's billing subscriptions.
type SubscriptionCanceler interface {
	CancelSubscriptions(ctx context.Context, customerID, subscriptionID string) error
}

type accountStorage interface {
	DeleteImage(ctx context.Context, objectName string) error
	DeletePrefix(ctx context.Context, prefix string) error
	GetSignedURLWithExpiry(ctx context.Context, objectName string, ttl time.Duration) (string, error)
}

// AccountService implements the AccountService gRPC service.
type AccountService struct {
	pb.UnimplementedAccountServiceServer
	db      *db.DB
	storage accountStorage
	billing SubscriptionCanceler
}

// NewAccountService creates an AccountService. storageClient and billing may be nil when unconfigured.
func NewAccountService(database *db.DB, storageClient *storage.Client, billing SubscriptionCanceler) *AccountService {
	s := &AccountService{db: database, billing: billing}
	if storageClient != nil {
		s.storage = storageClient
	}
	return s
}

// DeleteAccount cancels billing, deletes stored media, then deletes all account rows.
// Each step is idempotent so a failed call can be retried.
func (s *AccountService) DeleteAccount(ctx context.Context, req *pb.DeleteAccountRequest) (*pb.DeleteAccountResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	user, err := s.db.GetUser(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get user: %v", err)
	}
	if user == nil {
		return &pb.DeleteAccountResponse{Success: true}, nil
	}

	if !auth.IsM2MAuth(ctx) {
		if req.Password == "" {
			return nil, status.Error(codes.InvalidArgument, "password is required")
		}
		if user.FailedLoginAttempts >= 10 {
			return nil, status.Error(codes.PermissionDenied, "account is locked")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
			if recordErr := s.db.RecordFailedLogin(ctx, user.ID); recordErr != nil {
				return nil, status.Errorf(codes.Internal, "failed to record login attempt: %v", recordErr)
			}
			return nil, status.Error(codes.PermissionDenied, "incorrect password")
		}
	}

	l := logging.FromContext(ctx)

	customerID, subscriptionID := deref(user.StripeCustomerID), deref(user.StripeSubscriptionID)
	if customerID != "" || subscriptionID != "" {
		if s.billing == nil {
			return nil, status.Error(codes.FailedPrecondition, "billing not configured; cannot cancel subscription")
		}
		if err := s.billing.CancelSubscriptions(ctx, customerID, subscriptionID); err != nil {
			l.Errorw("stripe cancellation failed; account not deleted", "user_id", user.ID, "error", err)
			return nil, status.Errorf(codes.Unavailable, "failed to cancel subscription: %v", err)
		}
	}

	objects, err := s.db.UserMediaObjects(ctx, user.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list media: %v", err)
	}
	if s.storage == nil {
		if len(objects) > 0 || deref(user.ProfileImageGCSObject) != "" {
			return nil, status.Error(codes.FailedPrecondition, "storage not configured; cannot delete media")
		}
	} else {
		for _, name := range objects {
			if err := s.storage.DeleteImage(ctx, name); err != nil {
				return nil, status.Errorf(codes.Unavailable, "failed to delete media: %v", err)
			}
		}
		if p := deref(user.ProfileImageGCSObject); p != "" {
			if err := s.storage.DeleteImage(ctx, p); err != nil {
				return nil, status.Errorf(codes.Unavailable, "failed to delete profile image: %v", err)
			}
		}
		if err := s.storage.DeletePrefix(ctx, fmt.Sprintf("profiles/%s/", user.ID)); err != nil {
			return nil, status.Errorf(codes.Unavailable, "failed to delete profile images: %v", err)
		}
	}

	if err := s.db.DeleteUserData(ctx, user.ID); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete account: %v", err)
	}
	l.Infow("account deleted", "user_id", user.ID, "media_objects", len(objects))
	return &pb.DeleteAccountResponse{Success: true}, nil
}

// ExportData streams the user, their tags, then their notes in keyset-paginated pages.
// Media is returned as signed URLs rather than inline bytes so large libraries
// stay within gRPC message limits and server memory.
func (s *AccountService) ExportData(req *pb.ExportDataRequest, stream grpc.ServerStreamingServer[pb.ExportDataResponse]) error {
	ctx := stream.Context()
	if req.UserId == "" {
		return status.Error(codes.InvalidArgument, "user_id is required")
	}
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return err
	}

	user, err := s.db.GetUser(ctx, req.UserId)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get user: %v", err)
	}
	if user == nil {
		return status.Error(codes.NotFound, "user not found")
	}
	if err := stream.Send(&pb.ExportDataResponse{Item: &pb.ExportDataResponse_User{User: userToProto(user)}}); err != nil {
		return err
	}
	if p := deref(user.ProfileImageGCSObject); p != "" {
		url, err := s.signedURL(ctx, p)
		if err != nil {
			return err
		}
		if err := stream.Send(&pb.ExportDataResponse{Item: &pb.ExportDataResponse_ProfileImageUrl{ProfileImageUrl: url}}); err != nil {
			return err
		}
	}

	tags, err := s.db.ListTags(ctx, user.ID)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to list tags: %v", err)
	}
	for _, t := range tags {
		item := &pb.Tag{Id: t.ID, Name: t.Name, Count: clampInt32(t.Count), CreatedAt: timestamppb.New(t.CreatedAt)}
		if err := stream.Send(&pb.ExportDataResponse{Item: &pb.ExportDataResponse_Tag{Tag: item}}); err != nil {
			return err
		}
	}

	var cursor *db.ExportCursor
	for {
		notes, err := s.db.ExportNotes(ctx, user.ID, cursor, exportPageSize)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to export notes: %v", err)
		}
		for i := range notes {
			n, err := s.exportNote(ctx, &notes[i])
			if err != nil {
				return err
			}
			if err := stream.Send(&pb.ExportDataResponse{Item: &pb.ExportDataResponse_Note{Note: n}}); err != nil {
				return err
			}
		}
		if len(notes) < exportPageSize {
			return nil
		}
		last := notes[len(notes)-1]
		cursor = &db.ExportCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
}

func (s *AccountService) exportNote(ctx context.Context, n *db.Note) (*pb.Note, error) {
	out := &pb.Note{
		Id:        n.ID,
		Content:   n.Content,
		Tags:      make([]string, len(n.Tags)),
		CreatedAt: timestamppb.New(n.CreatedAt),
		UpdatedAt: timestamppb.New(n.UpdatedAt),
	}
	for i, t := range n.Tags {
		out.Tags[i] = t.Name
	}
	for _, img := range n.Images {
		url, err := s.signedURL(ctx, img.GCSObjectName)
		if err != nil {
			return nil, err
		}
		out.Images = append(out.Images, &pb.NoteImage{
			Id: img.ID, Url: url, ExtractedText: img.ExtractedText, MimeType: img.MimeType, CreatedAt: timestamppb.New(img.CreatedAt),
		})
	}
	for _, aud := range n.Audios {
		url, err := s.signedURL(ctx, aud.GCSObjectName)
		if err != nil {
			return nil, err
		}
		out.Audios = append(out.Audios, &pb.NoteAudio{
			Id: aud.ID, Url: url, TranscribedText: aud.TranscribedText, MimeType: aud.MimeType, CreatedAt: timestamppb.New(aud.CreatedAt),
		})
	}
	return out, nil
}

func (s *AccountService) signedURL(ctx context.Context, objectName string) (string, error) {
	if objectName == "" {
		return "", nil
	}
	if s.storage == nil {
		return "", status.Error(codes.FailedPrecondition, "storage not configured; cannot export media")
	}
	url, err := s.storage.GetSignedURLWithExpiry(ctx, objectName, exportURLExpiry)
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to sign media url: %v", err)
	}
	return url, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
