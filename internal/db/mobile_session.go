package db

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// MaxMobileSessions bounds persistent keys created by public Login per user.
const MaxMobileSessions = 10

// ErrMobileSessionLimit indicates that eviction could not free a session slot.
var ErrMobileSessionLimit = errors.New("mobile session limit reached")

const mobileSessionName = "etu-mobile"

// CreateMobileSession serializes issuance per user across server instances. At
// the cap it revokes the least-recently-used etu-mobile keys (never-used first,
// then oldest) in the same transaction. The generator runs only once a slot is
// free, and any failure rolls back both the eviction and the insert.
func (db *DB) CreateMobileSession(ctx context.Context, userID string, generate func() (prefix, hash string, err error)) (*ApiKey, error) {
	var key *ApiKey
	err := db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owner string
		if err := tx.Raw(`SELECT id FROM "User" WHERE id = ? FOR UPDATE`, userID).Row().Scan(&owner); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&ApiKey{}).Where(`"userId" = ? AND name = ?`, owner, mobileSessionName).Count(&count).Error; err != nil {
			return err
		}
		if excess := count - MaxMobileSessions + 1; excess > 0 {
			res := tx.Exec(`DELETE FROM "ApiKey" WHERE id IN (SELECT id FROM "ApiKey" WHERE "userId" = ? AND name = ? ORDER BY "lastUsed" ASC NULLS FIRST, "createdAt" ASC LIMIT ?)`, owner, mobileSessionName, excess)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected < excess {
				return ErrMobileSessionLimit
			}
		}
		prefix, hash, err := generate()
		if err != nil {
			return err
		}
		key, err = (&DB{conn: tx}).CreateApiKey(ctx, owner, mobileSessionName, prefix, hash)
		return err
	})
	if err != nil {
		return nil, err
	}
	return key, nil
}
