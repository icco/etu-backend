package db

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// MaxMobileSessions bounds persistent keys created by public Login per user.
const MaxMobileSessions = 10

// ErrMobileSessionLimit indicates that an old session must be revoked first.
var ErrMobileSessionLimit = errors.New("mobile session limit reached")

// CreateMobileSession serializes issuance per user across server instances. The
// generator runs only after the cap check, and any failure rolls back the insert.
func (db *DB) CreateMobileSession(ctx context.Context, userID string, generate func() (prefix, hash string, err error)) (*ApiKey, error) {
	var key *ApiKey
	err := db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owner string
		if err := tx.Raw(`SELECT id FROM "User" WHERE id = ? FOR UPDATE`, userID).Row().Scan(&owner); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&ApiKey{}).Where(`"userId" = ? AND name = ?`, owner, "etu-mobile").Count(&count).Error; err != nil {
			return err
		}
		if count >= MaxMobileSessions {
			return ErrMobileSessionLimit
		}
		prefix, hash, err := generate()
		if err != nil {
			return err
		}
		key, err = (&DB{conn: tx}).CreateApiKey(ctx, owner, "etu-mobile", prefix, hash)
		return err
	})
	if err != nil {
		return nil, err
	}
	return key, nil
}
