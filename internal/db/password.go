package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var errUserMissing = errors.New("user not found")

// ChangePassword stores a new password hash, clears failed logins, and revokes
// every API key and mobile session for the user in one transaction. It
// reports false when the user does not exist.
func (db *DB) ChangePassword(ctx context.Context, userID, newPassword string) (bool, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return false, fmt.Errorf("failed to hash password: %w", err)
	}
	err = db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&User{}).Where("id = ?", userID).Updates(map[string]interface{}{
			colPasswordHash:        string(hash),
			colFailedLoginAttempts: 0,
			colLastFailedLogin:     nil,
			colUpdatedAt:           time.Now(),
		})
		if result.Error != nil {
			return fmt.Errorf("failed to update password: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return errUserMissing
		}
		if err := tx.Where(`"userId" = ?`, userID).Delete(&ApiKey{}).Error; err != nil {
			return fmt.Errorf("failed to revoke API keys: %w", err)
		}
		return nil
	})
	if errors.Is(err, errUserMissing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
