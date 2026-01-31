package service

import (
	"context"

	"github.com/icco/etu-backend/internal/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// verifyUserAuthorization checks if the authenticated user is authorized to access the requested user's data
func verifyUserAuthorization(ctx context.Context, requestedUserID string) error {
	// M2M authentication can access any user (trusted service-to-service calls)
	if auth.IsM2MAuth(ctx) {
		return nil
	}

	// For regular API key auth, verify the user can only access their own data
	authenticatedUserID, err := auth.GetUserID(ctx)
	if err != nil {
		return status.Error(codes.Unauthenticated, "authentication required")
	}

	if authenticatedUserID != requestedUserID {
		return status.Error(codes.PermissionDenied, "cannot access another user's data")
	}

	return nil
}
