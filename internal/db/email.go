package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrDuplicateEmails indicates users whose emails differ only by case, which
// blocks creating the case-insensitive unique email index.
var ErrDuplicateEmails = errors.New("duplicate case-insensitive emails")

// ErrEmailTaken indicates another user already registered the email.
var ErrEmailTaken = errors.New("email already registered")

// NormalizeEmail trims and lowercases an email address for storage and lookup.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (db *DB) ensureEmailIndex(ctx context.Context) error {
	var dupes int64
	if err := db.conn.WithContext(ctx).Raw(`SELECT count(*) FROM (SELECT lower(email) FROM "User" GROUP BY lower(email) HAVING count(*) > 1) d`).Row().Scan(&dupes); err != nil {
		return fmt.Errorf("count duplicate emails: %w", err)
	}
	if dupes > 0 {
		return fmt.Errorf("%w: %d email addresses are shared by multiple users when compared case-insensitively; merge or rename them before starting the server", ErrDuplicateEmails, dupes)
	}
	if err := db.conn.WithContext(ctx).Exec(`CREATE UNIQUE INDEX IF NOT EXISTS "User_email_lower_key" ON "User" (lower(email))`).Error; err != nil {
		return fmt.Errorf("create lower(email) unique index: %w", err)
	}
	return nil
}
