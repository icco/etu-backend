package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrUserNotFound indicates the user does not exist.
	ErrUserNotFound = errors.New("user not found")
	// ErrAccountDisabled indicates the account is disabled.
	ErrAccountDisabled = errors.New("account is disabled")
	// ErrAccountLocked indicates the account is in a failed-login backoff window.
	ErrAccountLocked = errors.New("account is locked")
	// ErrWrongPassword indicates the supplied current password did not match.
	ErrWrongPassword = errors.New("current password is incorrect")
)

// ChangePasswordWithSettings locks the user row, verifies currentPassword,
// then stores the new password hash, clears failed logins, revokes every API
// key and mobile session, and applies UpdateUserSettings, all in one
// transaction. A wrong current password is recorded as a failed login.
func (db *DB) ChangePasswordWithSettings(ctx context.Context, userID, currentPassword, newPassword string, notionKey, name, notionDatabaseName, profileImageGCSObject *string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}
	var user *User
	wrongPassword := false
	err = db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked User
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", userID).First(&locked)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrUserNotFound
		}
		if result.Error != nil {
			return fmt.Errorf("failed to lock user: %w", result.Error)
		}
		if locked.Disabled {
			return ErrAccountDisabled
		}
		if LockoutRemaining(locked.FailedLoginAttempts, locked.LastFailedLogin, time.Now()) > 0 {
			return ErrAccountLocked
		}
		txDB := &DB{conn: tx}
		if bcrypt.CompareHashAndPassword([]byte(locked.PasswordHash), []byte(currentPassword)) != nil {
			wrongPassword = true
			return txDB.RecordFailedLogin(ctx, userID)
		}
		if err := tx.Model(&User{}).Where("id = ?", userID).Updates(map[string]interface{}{
			colPasswordHash:        string(hash),
			colFailedLoginAttempts: 0,
			colLastFailedLogin:     nil,
			colUpdatedAt:           time.Now(),
		}).Error; err != nil {
			return fmt.Errorf("failed to update password: %w", err)
		}
		if err := tx.Where(`"userId" = ?`, userID).Delete(&ApiKey{}).Error; err != nil {
			return fmt.Errorf("failed to revoke API keys: %w", err)
		}
		var err error
		user, err = txDB.UpdateUserSettings(ctx, userID, notionKey, name, nil, notionDatabaseName, profileImageGCSObject)
		return err
	})
	if err != nil {
		return nil, err
	}
	if wrongPassword {
		return nil, ErrWrongPassword
	}
	return user, nil
}
