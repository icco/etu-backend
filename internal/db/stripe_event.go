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
	var user *User
	err := db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&models.StripeEvent{ID: eventID})
		if result.Error != nil {
			return fmt.Errorf("record stripe event: %w", result.Error)
		}
		txDB := &DB{conn: tx}
		var err error
		if result.RowsAffected == 0 {
			user, err = txDB.GetUser(ctx, userID)
		} else {
			user, err = update(txDB)
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
