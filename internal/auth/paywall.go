package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"go.icco.me/etu-backend/internal/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultPaywallGrace is how long a past_due subscription keeps full access.
const DefaultPaywallGrace = 7 * 24 * time.Hour

var paywallExemptMethods = map[string]bool{
	"/etu.AuthService/GetUser":                    true,
	"/etu.UserSettingsService/GetUserSettings":    true,
	"/etu.UserSettingsService/GetProfileImageURL": true,
	"/etu.ApiKeysService/ListApiKeys":             true,
	"/etu.ApiKeysService/DeleteApiKey":            true,
	"/etu.AccountService/DeleteAccount":           true,
	"/etu.AccountService/ExportData":              true,
}

// AccountState is the subset of a user record that drives access decisions.
type AccountState struct {
	Status       string
	Disabled     bool
	PastDueSince *time.Time
}

// Paywall decides which RPCs an API-key user may call based on billing state.
type Paywall struct {
	Grace time.Duration
}

// NewPaywallFromEnv builds a Paywall using PAYWALL_GRACE_PERIOD (Go duration), defaulting to DefaultPaywallGrace.
func NewPaywallFromEnv() (Paywall, error) {
	raw := os.Getenv("PAYWALL_GRACE_PERIOD")
	if raw == "" {
		return Paywall{Grace: DefaultPaywallGrace}, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return Paywall{}, fmt.Errorf("invalid PAYWALL_GRACE_PERIOD %q: %w", raw, err)
	}
	if d < 0 {
		return Paywall{}, fmt.Errorf("PAYWALL_GRACE_PERIOD must not be negative: %q", raw)
	}
	return Paywall{Grace: d}, nil
}

// Authorize returns a PermissionDenied status error when the account may not call method.
func (p Paywall) Authorize(method string, s AccountState, now time.Time) error {
	if s.Disabled {
		return status.Error(codes.PermissionDenied, "account disabled")
	}
	if p.hasFullAccess(s, now) || paywallExemptMethods[method] {
		return nil
	}
	return status.Error(codes.PermissionDenied, "subscription required")
}

func (p Paywall) hasFullAccess(s AccountState, now time.Time) bool {
	if s.Status == db.SubscriptionStatusPastDue {
		return s.PastDueSince != nil && now.Before(s.PastDueSince.Add(p.Grace))
	}
	return slices.Contains(db.AIEligibleSubscriptionStatuses, s.Status)
}

// GetAccountState loads the billing and lockout state for userID.
func (a *Authenticator) GetAccountState(ctx context.Context, userID string) (AccountState, error) {
	var s AccountState
	var since sql.NullTime
	err := a.db.QueryRowContext(ctx, `SELECT "subscriptionStatus", "disabled", "pastDueSince" FROM "User" WHERE id = $1`, userID).
		Scan(&s.Status, &s.Disabled, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountState{}, fmt.Errorf("user %q not found", userID)
	}
	if err != nil {
		return AccountState{}, fmt.Errorf("failed to load account state: %w", err)
	}
	if since.Valid {
		t := since.Time
		s.PastDueSince = &t
	}
	return s, nil
}
