package service

import (
	"context"
	"errors"

	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/billing"
	pb "go.icco.me/etu-backend/proto"
	"go.icco.me/gutil/logging"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const playProductID = "etu"

// BillingService implements the BillingService gRPC service.
type BillingService struct {
	pb.UnimplementedBillingServiceServer
	sync        *PlaySync
	packageName string
}

// NewBillingService creates a BillingService. sync may be nil when Google Play billing is not configured.
func NewBillingService(sync *PlaySync, packageName string) *BillingService {
	return &BillingService{sync: sync, packageName: packageName}
}

// VerifyPlayPurchase verifies a Google Play subscription purchase and applies it to the calling user.
func (s *BillingService) VerifyPlayPurchase(ctx context.Context, req *pb.VerifyPlayPurchaseRequest) (*pb.VerifyPlayPurchaseResponse, error) {
	if auth.IsM2MAuth(ctx) {
		return nil, status.Error(codes.PermissionDenied, "api key required")
	}
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if s.sync == nil || s.packageName == "" {
		return nil, status.Error(codes.FailedPrecondition, "google play billing not configured")
	}
	if req.GetPurchaseToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "purchase_token is required")
	}
	if req.GetProductId() != playProductID {
		return nil, status.Error(codes.InvalidArgument, "unknown product_id")
	}

	user, err := s.sync.Sync(ctx, req.GetPurchaseToken(), userID)
	if err != nil {
		l := logging.FromContext(ctx)
		switch {
		case errors.Is(err, billing.ErrPlayPurchaseNotFound):
			return nil, status.Error(codes.NotFound, "play purchase not found")
		case errors.Is(err, ErrPlayAccountMismatch):
			l.Warnw("play purchase account mismatch", "user_id", userID, "error", err)
			return nil, status.Error(codes.PermissionDenied, "purchase belongs to another account")
		case errors.Is(err, ErrTakeoverFailed):
			l.Errorw("play takeover failed", "user_id", userID, "error", err)
			return nil, status.Error(codes.Unavailable, "could not cancel the existing subscription; retry later")
		case errors.Is(err, ErrPlayTestPurchase):
			return nil, status.Error(codes.FailedPrecondition, "test purchases are not accepted")
		case errors.Is(err, ErrPlayWrongProduct):
			return nil, status.Error(codes.InvalidArgument, "purchase is not for a supported product")
		case errors.Is(err, ErrPlayUnmatched):
			return nil, status.Error(codes.NotFound, "user not found")
		default:
			return nil, status.Errorf(codes.Internal, "failed to verify play purchase: %v", err)
		}
	}
	return &pb.VerifyPlayPurchaseResponse{User: userToProto(user)}, nil
}
