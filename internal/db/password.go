package db

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// UpdateUserSettingsWithPassword applies UpdateUserSettings and, when
// newPassword is non-empty, stores its hash, clears failed logins, and revokes
// every API key and mobile session, all in one transaction.
func (db *DB) UpdateUserSettingsWithPassword(ctx context.Context, userID string, notionKey, name, newPassword, notionDatabaseName, profileImageGCSObject *string) (*User, error) {
	var hash []byte
	if newPassword != nil && *newPassword != "" {
		var err error
		hash, err = bcrypt.GenerateFromPassword([]byte(*newPassword), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("failed to hash password: %w", err)
		}
	}
	if hash == nil {
		return db.UpdateUserSettings(ctx, userID, notionKey, name, nil, notionDatabaseName, profileImageGCSObject)
	}
	var user *User
	err := db.conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			return fmt.Errorf("user not found")
		}
		if err := tx.Where(`"userId" = ?`, userID).Delete(&ApiKey{}).Error; err != nil {
			return fmt.Errorf("failed to revoke API keys: %w", err)
		}
		var err error
		user, err = (&DB{conn: tx}).UpdateUserSettings(ctx, userID, notionKey, name, nil, notionDatabaseName, profileImageGCSObject)
		return err
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}
