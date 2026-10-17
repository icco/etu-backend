package db

import (
	"context"
	"errors"
	"fmt"

	"go.icco.me/etu-backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (db *DB) withStripeEvent(ctx context.Context, userID, eventID string, update func(*DB) (*User, error)) (*User, error) {
	if eventID == "" {
		return update(db)
	}
	return db.stripeEventTx(ctx, userID, eventID, update)
}

// stripeEventTx runs update in a transaction that also records eventID, when
// set. A duplicate event returns the current user without running update.
func (db *DB) stripeEventTx(ctx context.Context, userID, eventID string, update func(*DB) (*User, error)) (*User, error) {
	var user *User
	err := db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fresh := true
		if eventID != "" {
			result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&models.StripeEvent{ID: eventID})
			if result.Error != nil {
				return fmt.Errorf("record stripe event: %w", result.Error)
			}
			fresh = result.RowsAffected > 0
		}
		txDB := &DB{conn: tx, paywallGrace: db.paywallGrace}
		var err error
		if fresh {
			user, err = update(txDB)
		} else {
			user, err = txDB.GetUser(ctx, userID)
		}
		if err != nil {
			return err
		}
		if user == nil {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

// UpdateUserSubscriptionCancelingPlay is UpdateUserSubscription for a paid
// Stripe status. In one transaction it records the event, locks the user, and,
// only when the write will apply, passes the user's active Play purchases to
// cancelPlay before writing, so Play cannot keep charging a Stripe subscriber.
// A cancelPlay error aborts the write and leaves the event unrecorded.
func (db *DB) UpdateUserSubscriptionCancelingPlay(ctx context.Context, userID string, in SubscriptionUpdate, cancelPlay func(context.Context, []PlaySubscription) error) (*User, error) {
	return db.stripeEventTx(ctx, userID, in.StripeEventID, func(tx *DB) (*User, error) {
		writable, err := tx.lockStripeWritable(ctx, userID, in.ExpectedStripeCustomerID)
		if err != nil {
			return nil, err
		}
		if writable {
			subs, err := tx.ActivePlaySubscriptions(ctx, userID)
			if err != nil {
				return nil, err
			}
			if len(subs) > 0 {
				if err := cancelPlay(ctx, subs); err != nil {
					return nil, err
				}
			}
		}
		return tx.updateUserSubscription(ctx, userID, in)
	})
}

// lockStripeWritable locks the user row and reports whether a Stripe
// subscription write for it would apply.
func (db *DB) lockStripeWritable(ctx context.Context, userID, expectedCustomerID string) (bool, error) {
	query := ownedBy(db.conn.WithContext(ctx).Model(&User{}).Where("id = ?", userID), BillingProviderStripe, db.paywallGrace)
	if expectedCustomerID != "" {
		query = query.Where(`"stripeCustomerId" = ?`, expectedCustomerID)
	}
	var ids []string
	if err := query.Clauses(clause.Locking{Strength: "UPDATE"}).Limit(1).Pluck("id", &ids).Error; err != nil {
		return false, fmt.Errorf("failed to lock user for stripe update: %w", err)
	}
	return len(ids) > 0, nil
}
