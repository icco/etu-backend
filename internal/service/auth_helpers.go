package service

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type contextKey string

const (
	userIDContextKey   contextKey = "userID"
	authTypeContextKey contextKey = "authType"
)

// getAuthenticatedUserID extracts the authenticated user ID from the context
func getAuthenticatedUserID(ctx context.Context) (string, error) {
	userID, ok := ctx.Value(userIDContextKey).(string)
	if !ok || userID == "" {
		return "", fmt.Errorf("user ID not found in context")
	}
	return userID, nil
}

// isM2MAuth checks if the request is authenticated via M2M token
func isM2MAuth(ctx context.Context) bool {
	authType, ok := ctx.Value(authTypeContextKey).(string)
	return ok && authType == "m2m"
}

// verifyUserAuthorization checks if the authenticated user is authorized to access the requested user's data
func verifyUserAuthorization(ctx context.Context, requestedUserID string) error {
	// M2M authentication can access any user
	if isM2MAuth(ctx) {
		return nil
	}

	// For regular API key auth, verify the user can only access their own data
	authenticatedUserID, err := getAuthenticatedUserID(ctx)
	if err != nil {
		return status.Error(codes.Unauthenticated, "authentication required")
	}

	if authenticatedUserID != requestedUserID {
		return status.Error(codes.PermissionDenied, "cannot access another user's data")
	}

	return nil
}
