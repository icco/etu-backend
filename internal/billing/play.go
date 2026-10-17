package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/api/androidpublisher/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// ErrPlayPurchaseNotFound is returned when Google Play no longer knows the
// purchase token.
var ErrPlayPurchaseNotFound = errors.New("play purchase not found")

const (
	playStateActive          = "SUBSCRIPTION_STATE_ACTIVE"
	playStateCanceled        = "SUBSCRIPTION_STATE_CANCELED"
	playStateInGracePeriod   = "SUBSCRIPTION_STATE_IN_GRACE_PERIOD"
	playStateOnHold          = "SUBSCRIPTION_STATE_ON_HOLD"
	playStatePaused          = "SUBSCRIPTION_STATE_PAUSED"
	playStateExpired         = "SUBSCRIPTION_STATE_EXPIRED"
	playStatePendingCanceled = "SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED"

	playAcknowledged = "ACKNOWLEDGEMENT_STATE_ACKNOWLEDGED"
)

// PlayPurchase is the subset of a Google Play subscription purchase that etu
// stores. State is the raw Play subscription state enum.
type PlayPurchase struct {
	State               string
	ProductID           string
	BasePlanID          string
	ExpiryTime          *time.Time
	AutoRenewing        bool
	Acknowledged        bool
	Test                bool
	FreeTrial           bool
	ObfuscatedAccountID string
	LinkedPurchaseToken *string
}

// PlayAPI reads and manages Google Play subscription purchases.
type PlayAPI interface {
	GetSubscription(ctx context.Context, token string) (*PlayPurchase, error)
	Acknowledge(ctx context.Context, productID, token string) error
	Cancel(ctx context.Context, token string) error
}

// Play talks to the Google Play Developer API for one app.
type Play struct {
	svc         *androidpublisher.Service
	packageName string
}

var _ PlayAPI = (*Play)(nil)

// NewPlay returns a Play client for the given package name using Application
// Default Credentials.
func NewPlay(ctx context.Context, packageName string) (*Play, error) {
	return newPlayWithOptions(ctx, packageName)
}

func newPlayWithOptions(ctx context.Context, packageName string, opts ...option.ClientOption) (*Play, error) {
	svc, err := androidpublisher.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create play androidpublisher service: %w", err)
	}
	return &Play{svc: svc, packageName: packageName}, nil
}

// GetSubscription fetches the purchase for the token. It returns
// ErrPlayPurchaseNotFound when Play reports 404 or 410.
func (p *Play) GetSubscription(ctx context.Context, token string) (*PlayPurchase, error) {
	sub, err := p.svc.Purchases.Subscriptionsv2.Get(p.packageName, token).Context(ctx).Do()
	if err != nil {
		return nil, playError("get play subscription", err)
	}
	return toPlayPurchase(sub)
}

// Acknowledge acknowledges the purchase so Play does not refund it.
func (p *Play) Acknowledge(ctx context.Context, productID, token string) error {
	err := p.svc.Purchases.Subscriptions.
		Acknowledge(p.packageName, productID, token, &androidpublisher.SubscriptionPurchasesAcknowledgeRequest{}).
		Context(ctx).Do()
	return playError("acknowledge play subscription", err)
}

// Cancel stops payments for the subscription so it cannot be restored.
func (p *Play) Cancel(ctx context.Context, token string) error {
	req := &androidpublisher.CancelSubscriptionPurchaseRequest{
		CancellationContext: &androidpublisher.CancellationContext{
			CancellationType: "DEVELOPER_REQUESTED_STOP_PAYMENTS",
		},
	}
	_, err := p.svc.Purchases.Subscriptionsv2.Cancel(p.packageName, token, req).Context(ctx).Do()
	return playError("cancel play subscription", err)
}

func playError(op string, err error) error {
	if err == nil {
		return nil
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) && (ge.Code == http.StatusNotFound || ge.Code == http.StatusGone) {
		return fmt.Errorf("%s: %w: %w", op, ErrPlayPurchaseNotFound, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

func toPlayPurchase(sub *androidpublisher.SubscriptionPurchaseV2) (*PlayPurchase, error) {
	out := &PlayPurchase{
		State:        sub.SubscriptionState,
		Acknowledged: sub.AcknowledgementState == playAcknowledged,
		Test:         sub.TestPurchase != nil,
	}
	if sub.ExternalAccountIdentifiers != nil {
		out.ObfuscatedAccountID = sub.ExternalAccountIdentifiers.ObfuscatedExternalAccountId
	}
	if sub.LinkedPurchaseToken != "" {
		out.LinkedPurchaseToken = &sub.LinkedPurchaseToken
	}
	for i, li := range sub.LineItems {
		if li == nil {
			continue
		}
		if i == 0 {
			out.ProductID = li.ProductId
			if li.OfferDetails != nil {
				out.BasePlanID = li.OfferDetails.BasePlanId
			}
		}
		if li.AutoRenewingPlan != nil && li.AutoRenewingPlan.AutoRenewEnabled {
			out.AutoRenewing = true
		}
		if li.OfferPhase != nil && li.OfferPhase.FreeTrial != nil {
			out.FreeTrial = true
		}
		if li.ExpiryTime == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, li.ExpiryTime)
		if err != nil {
			return nil, fmt.Errorf("parse play expiry time %q: %w", li.ExpiryTime, err)
		}
		if out.ExpiryTime == nil || t.After(*out.ExpiryTime) {
			out.ExpiryTime = &t
		}
	}
	return out, nil
}

// MapPlayStatus converts a Play purchase into a subscription status and
// whether it ends at the period end. An empty status means the purchase
// carries no state change, as for pending purchases.
func MapPlayStatus(p *PlayPurchase) (status string, cancelAtPeriodEnd bool) {
	switch p.State {
	case playStateActive:
		if p.FreeTrial {
			return "trialing", false
		}
		return "active", false
	case playStateCanceled:
		return "active", true
	case playStateInGracePeriod:
		return "past_due", false
	case playStateOnHold, playStatePaused:
		return "unpaid", false
	case playStateExpired, playStatePendingCanceled:
		return "canceled", false
	default:
		return "", false
	}
}
