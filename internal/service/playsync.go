package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.icco.me/etu-backend/internal/billing"
	"go.icco.me/etu-backend/internal/db"
	"go.icco.me/gutil/logging"
)

var (
	// ErrPlayAccountMismatch is returned when a Play purchase belongs to a different Etu user.
	ErrPlayAccountMismatch = errors.New("play purchase belongs to another account")
	// ErrTakeoverFailed is returned when the other provider's subscription could not be canceled before a store switch.
	ErrTakeoverFailed = errors.New("could not cancel the existing subscription")
	// ErrPlayTestPurchase is returned for a Play test purchase when test purchases are not allowed.
	ErrPlayTestPurchase = errors.New("play test purchases are not accepted")
	// ErrPlayWrongProduct is returned when a Play purchase is not for the etu subscription product.
	ErrPlayWrongProduct = errors.New("play purchase is for another product")
	// ErrPlayUnmatched is returned when a Play purchase cannot be tied to an existing user.
	ErrPlayUnmatched = errors.New("play purchase has no matching user")
)

// PlaySync applies Google Play subscription state to Etu users.
type PlaySync struct {
	DB        *db.DB
	Play      billing.PlayAPI
	Stripe    SubscriptionCanceler
	AllowTest bool
	Grace     time.Duration
}

// Sync fetches the purchase for token from Google Play and applies it to its
// user. When expectedUserID is set the purchase must belong to that user;
// otherwise the user is the one the token is stored for, or else the
// purchase's obfuscated account ID. A paying purchase for a Stripe-owned user
// cancels the Stripe subscription first and fails with ErrTakeoverFailed if
// that cancel fails.
func (p *PlaySync) Sync(ctx context.Context, token, expectedUserID string) (*db.User, error) {
	purchase, err := p.Play.GetSubscription(ctx, token)
	if err != nil {
		return nil, err
	}
	if purchase.Test && !p.AllowTest {
		return nil, ErrPlayTestPurchase
	}
	if purchase.ProductID != playProductID {
		return nil, ErrPlayWrongProduct
	}
	if expectedUserID != "" && purchase.ObfuscatedAccountID != expectedUserID {
		return nil, ErrPlayAccountMismatch
	}

	stored, err := p.DB.PlaySubscriptionUser(ctx, token)
	if err != nil {
		return nil, err
	}
	userID := expectedUserID
	switch {
	case userID != "" && stored != "" && stored != userID:
		return nil, ErrPlayAccountMismatch
	case userID == "" && stored != "":
		userID = stored
	case userID == "":
		userID = purchase.ObfuscatedAccountID
	}
	if userID == "" {
		return nil, ErrPlayUnmatched
	}
	owner, err := p.DB.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if owner == nil {
		return nil, ErrPlayUnmatched
	}

	subStatus, cancelAtPeriodEnd := billing.MapPlayStatus(purchase)
	paying := !cancelAtPeriodEnd && (subStatus == "active" || subStatus == "trialing")
	if paying && deref(owner.BillingProvider) == db.BillingProviderStripe {
		if err := p.takeOverStripe(ctx, owner); err != nil {
			return nil, err
		}
	}

	in := db.PlaySubscriptionUpdate{
		PurchaseToken:       token,
		UserID:              userID,
		ProductID:           purchase.ProductID,
		BasePlanID:          purchase.BasePlanID,
		State:               purchase.State,
		ExpiryTime:          purchase.ExpiryTime,
		AutoRenewing:        purchase.AutoRenewing,
		Acknowledged:        purchase.Acknowledged,
		LinkedPurchaseToken: purchase.LinkedPurchaseToken,
		Status:              subStatus,
		CancelAtPeriodEnd:   cancelAtPeriodEnd,
	}
	user, err := p.DB.ApplyPlaySubscription(ctx, in, p.Grace)
	if errors.Is(err, db.ErrBillingProviderMismatch) && paying {
		user, err = p.retryTakeover(ctx, in)
	}
	switch {
	case errors.Is(err, db.ErrPlayTokenUserMismatch):
		return nil, fmt.Errorf("%w: %w", ErrPlayAccountMismatch, err)
	case errors.Is(err, db.ErrBillingProviderMismatch):
		logging.FromContext(ctx).Infow("play purchase recorded; stripe still owns the user", "user_id", userID, "state", purchase.State)
		return p.currentUser(ctx, userID)
	case err != nil:
		return nil, err
	}

	if purchase.Acknowledged || !grantsAccess(subStatus) {
		return user, nil
	}
	if err := p.Play.Acknowledge(ctx, purchase.ProductID, token); err != nil {
		return nil, err
	}
	if err := p.DB.MarkPlayAcknowledged(ctx, token); err != nil {
		return nil, err
	}
	return user, nil
}

func (p *PlaySync) retryTakeover(ctx context.Context, in db.PlaySubscriptionUpdate) (*db.User, error) {
	owner, err := p.currentUser(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	if err := p.takeOverStripe(ctx, owner); err != nil {
		return nil, err
	}
	user, err := p.DB.ApplyPlaySubscription(ctx, in, p.Grace)
	if errors.Is(err, db.ErrBillingProviderMismatch) {
		return nil, fmt.Errorf("%w: user still owned by another provider", ErrTakeoverFailed)
	}
	return user, err
}

func (p *PlaySync) currentUser(ctx context.Context, userID string) (*db.User, error) {
	user, err := p.DB.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrPlayUnmatched
	}
	return user, nil
}

func (p *PlaySync) takeOverStripe(ctx context.Context, owner *db.User) error {
	if p.Stripe == nil {
		return fmt.Errorf("%w: stripe billing not configured", ErrTakeoverFailed)
	}
	if err := p.Stripe.CancelSubscriptions(ctx, deref(owner.StripeCustomerID), deref(owner.StripeSubscriptionID)); err != nil {
		return fmt.Errorf("%w: %w", ErrTakeoverFailed, err)
	}
	return p.DB.ReleaseStripeOwnership(ctx, owner.ID)
}

func grantsAccess(subStatus string) bool {
	return subStatus == "active" || subStatus == "trialing" || subStatus == db.SubscriptionStatusPastDue
}

// cancelPlaySubscriptions stops payments for each subscription whose current
// Play state can still charge the user. Purchases Play no longer knows are skipped.
func cancelPlaySubscriptions(ctx context.Context, play billing.PlayAPI, subs []db.PlaySubscription) error {
	for _, sub := range subs {
		purchase, err := play.GetSubscription(ctx, sub.PurchaseToken)
		if errors.Is(err, billing.ErrPlayPurchaseNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if !billing.PlayStateCancelable(purchase.State) {
			continue
		}
		if err := play.Cancel(ctx, sub.PurchaseToken); err != nil {
			return err
		}
	}
	return nil
}

// HandleNotification applies a Google Play real-time notification for token.
// A voided purchase forces its user to canceled without consulting Play. Errors
// that retrying cannot fix wrap billing.ErrRTDNPermanent.
func (p *PlaySync) HandleNotification(ctx context.Context, token string, voided bool) error {
	if voided {
		return p.voidPurchase(ctx, token)
	}
	_, err := p.Sync(ctx, token, "")
	for _, permanent := range []error{billing.ErrPlayPurchaseNotFound, ErrPlayUnmatched, ErrPlayAccountMismatch, ErrPlayWrongProduct, ErrPlayTestPurchase} {
		if errors.Is(err, permanent) {
			return fmt.Errorf("%w: %w", billing.ErrRTDNPermanent, err)
		}
	}
	return err
}

func (p *PlaySync) voidPurchase(ctx context.Context, token string) error {
	_, err := p.DB.VoidPlaySubscription(ctx, token, p.Grace)
	switch {
	case errors.Is(err, db.ErrPlaySubscriptionNotFound):
		return fmt.Errorf("%w: %w", billing.ErrRTDNPermanent, err)
	case errors.Is(err, db.ErrBillingProviderMismatch):
		logging.FromContext(ctx).Infow("voided play purchase left the user untouched; stripe owns the subscription")
		return nil
	}
	return err
}
