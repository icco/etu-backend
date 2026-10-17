package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.icco.me/etu-backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// BillingProviderStripe marks a user whose subscription is managed by Stripe.
	BillingProviderStripe = "stripe"
	// BillingProviderPlay marks a user whose subscription is managed by Google Play.
	BillingProviderPlay = "play"

	playStateExpired               = "SUBSCRIPTION_STATE_EXPIRED"
	playStatePendingPurchaseCancel = "SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED"

	defaultPaywallGrace = 7 * 24 * time.Hour
)

// ErrBillingProviderMismatch is returned when a billing write is refused
// because the other billing provider owns a paid subscription for the user.
var ErrBillingProviderMismatch = errors.New("billing managed by google play")

// PlaySubscription is an alias for models.PlaySubscription.
type PlaySubscription = models.PlaySubscription

// SetPaywallGrace sets how long a past_due subscription counts as paid when
// deciding which billing provider owns a user.
func (db *DB) SetPaywallGrace(d time.Duration) {
	db.paywallGrace = d
}

// ownershipClause returns the WHERE fragment, with its arguments, that lets
// provider write a user's billing fields: nobody owns the user, provider
// already does, or the user's current subscription is not paid.
func ownershipClause(provider string, grace time.Duration) (string, []any) {
	paid := make([]string, 0, len(AIEligibleSubscriptionStatuses))
	for _, s := range AIEligibleSubscriptionStatuses {
		if s != SubscriptionStatusPastDue {
			paid = append(paid, s)
		}
	}
	sql := `"billingProvider" IS NULL OR "billingProvider" = ? OR NOT COALESCE(` +
		`("subscriptionStatus" IN ? AND "subscriptionStatus" <> 'past_due') OR ` +
		`("subscriptionStatus" = 'past_due' AND "pastDueSince" > now() - make_interval(secs => ?)), false)`
	return sql, []any{provider, paid, grace.Seconds()}
}

func ownedBy(q *gorm.DB, provider string, grace time.Duration) *gorm.DB {
	sql, args := ownershipClause(provider, grace)
	return q.Where("("+sql+")", args...)
}

// PlaySubscriptionUpdate carries one Google Play subscription state written by ApplyPlaySubscription.
// An empty Status leaves the user's subscription fields untouched.
type PlaySubscriptionUpdate struct {
	PurchaseToken       string
	UserID              string
	ProductID           string
	BasePlanID          string
	State               string
	ExpiryTime          *time.Time
	AutoRenewing        bool
	Acknowledged        bool
	LinkedPurchaseToken *string
	Status              string
	CancelAtPeriodEnd   bool
}

// ApplyPlaySubscription records a Play purchase and, when allowed by the
// provider ownership rule, applies its status to the user. It returns
// ErrBillingProviderMismatch when another provider owns a paid subscription;
// the purchase row is still recorded in that case.
func (db *DB) ApplyPlaySubscription(ctx context.Context, in PlaySubscriptionUpdate, grace time.Duration) (*User, error) {
	var blocked bool
	err := db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := models.PlaySubscription{
			PurchaseToken:       in.PurchaseToken,
			UserID:              in.UserID,
			ProductID:           in.ProductID,
			BasePlanID:          in.BasePlanID,
			State:               in.State,
			ExpiryTime:          in.ExpiryTime,
			AutoRenewing:        in.AutoRenewing,
			Acknowledged:        in.Acknowledged,
			LinkedPurchaseToken: in.LinkedPurchaseToken,
		}
		upsert := tx.Omit(clause.Associations).Clauses(
			clause.OnConflict{
				Columns: []clause.Column{{Name: "purchaseToken"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"productId", "basePlanId", "state", "expiryTime", "autoRenewing",
					"acknowledged", "linkedPurchaseToken", colUpdatedAt,
				}),
			},
			clause.Returning{Columns: []clause.Column{{Name: "superseded"}}},
		).Create(&row)
		if upsert.Error != nil {
			return fmt.Errorf("failed to upsert play subscription: %w", upsert.Error)
		}

		if in.LinkedPurchaseToken != nil {
			result := tx.Model(&models.PlaySubscription{}).
				Where(`"purchaseToken" = ? AND "userId" = ?`, *in.LinkedPurchaseToken, in.UserID).
				Updates(map[string]any{"superseded": true, colUpdatedAt: time.Now().UTC()})
			if result.Error != nil {
				return fmt.Errorf("failed to supersede linked play subscription: %w", result.Error)
			}
		}

		if row.Superseded || in.Status == "" {
			return nil
		}

		updates := map[string]any{
			colSubscriptionStatus: in.Status,
			colCancelAtPeriodEnd:  in.CancelAtPeriodEnd,
			colBillingProvider:    BillingProviderPlay,
		}
		if in.Status == SubscriptionStatusPastDue {
			updates[colPastDueSince] = gorm.Expr(`COALESCE("pastDueSince", now())`)
		} else {
			updates[colPastDueSince] = gorm.Expr("NULL")
		}
		if in.ExpiryTime != nil {
			updates[colSubscriptionEnd] = *in.ExpiryTime
		}
		result := ownedBy(tx.Model(&User{}).Where("id = ?", in.UserID), BillingProviderPlay, grace).Updates(updates)
		if result.Error != nil {
			return fmt.Errorf("failed to update user play subscription: %w", result.Error)
		}
		blocked = result.RowsAffected == 0
		return nil
	})
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, ErrBillingProviderMismatch
	}
	return db.GetUser(ctx, in.UserID)
}

// PlaySubscriptionUser returns the user a purchase token belongs to, or an empty string when the token is unknown.
func (db *DB) PlaySubscriptionUser(ctx context.Context, purchaseToken string) (string, error) {
	var ids []string
	err := db.conn.WithContext(ctx).Model(&models.PlaySubscription{}).
		Where(`"purchaseToken" = ?`, purchaseToken).Limit(1).Pluck(`"userId"`, &ids).Error
	if err != nil {
		return "", fmt.Errorf("failed to look up play subscription user: %w", err)
	}
	if len(ids) == 0 {
		return "", nil
	}
	return ids[0], nil
}

// ActivePlaySubscriptions returns the user's Play purchases that are neither superseded nor expired or canceled before purchase.
func (db *DB) ActivePlaySubscriptions(ctx context.Context, userID string) ([]PlaySubscription, error) {
	var subs []PlaySubscription
	err := db.conn.WithContext(ctx).
		Where(`"userId" = ? AND NOT "superseded" AND "state" NOT IN ?`, userID, []string{playStateExpired, playStatePendingPurchaseCancel}).
		Find(&subs).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list active play subscriptions: %w", err)
	}
	return subs, nil
}
