package auth

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

// ContextKey is the type used for context keys in authentication.
// Exported so all packages use the same type for context key comparison.
type ContextKey string

const (
	// UserIDKey is the context key for storing the authenticated user ID
	UserIDKey ContextKey = "userID"
	// AuthTypeKey is the context key for storing the authentication type
	AuthTypeKey ContextKey = "authType"
)

// GetUserID extracts the user ID from context
func GetUserID(ctx context.Context) (string, error) {
	userID, ok := ctx.Value(UserIDKey).(string)
	if !ok || userID == "" {
		return "", fmt.Errorf("user ID not found in context")
	}
	return userID, nil
}

// GetAuthType extracts the authentication type from context ("m2m" or "apikey")
func GetAuthType(ctx context.Context) string {
	authType, ok := ctx.Value(AuthTypeKey).(string)
	if !ok {
		return ""
	}
	return authType
}

// IsM2MAuth returns true if the request was authenticated via M2M token
func IsM2MAuth(ctx context.Context) bool {
	return GetAuthType(ctx) == "m2m"
}

// SetAuthContext adds authentication info to the context
func SetAuthContext(ctx context.Context, userID, authType string) context.Context {
	ctx = context.WithValue(ctx, UserIDKey, userID)
	ctx = context.WithValue(ctx, AuthTypeKey, authType)
	return ctx
}

// Authenticator handles API key authentication
type Authenticator struct {
	db *sql.DB
}

// New creates a new Authenticator
func New() (*Authenticator, error) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable not set")
	}

	conn, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &Authenticator{db: conn}, nil
}

// Close closes the database connection
func (a *Authenticator) Close() error {
	return a.db.Close()
}

// VerifyAPIKey verifies an API key and returns the associated user ID
// API keys have the format: etu_<64 hex characters>
func (a *Authenticator) VerifyAPIKey(ctx context.Context, apiKey string) (string, error) {
	// Validate key format
	if !strings.HasPrefix(apiKey, "etu_") {
		return "", fmt.Errorf("invalid API key format")
	}

	// Extract prefix for lookup (first 12 chars of the key)
	keyPrefix := apiKey[:12]

	// Find API key records matching the prefix
	rows, err := a.db.QueryContext(ctx, `
		SELECT id, "keyHash", "userId"
		FROM "ApiKey"
		WHERE "keyPrefix" = $1
	`, keyPrefix)
	if err != nil {
		return "", fmt.Errorf("failed to query API keys: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("Error closing rows: %v", err)
		}
	}()

	// Check each potential match
	for rows.Next() {
		var id, keyHash, userID string
		if err := rows.Scan(&id, &keyHash, &userID); err != nil {
			return "", fmt.Errorf("failed to scan API key: %w", err)
		}

		// Compare the full key against the hash
		if err := bcrypt.CompareHashAndPassword([]byte(keyHash), []byte(apiKey)); err == nil {
			// Update last used timestamp
			go a.updateLastUsed(id)
			return userID, nil
		}
	}

	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("error iterating API keys: %w", err)
	}

	return "", fmt.Errorf("invalid API key")
}

// updateLastUsed updates the lastUsed timestamp for an API key
func (a *Authenticator) updateLastUsed(keyID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = a.db.ExecContext(ctx, `
		UPDATE "ApiKey" SET "lastUsed" = $1 WHERE id = $2
	`, time.Now(), keyID)
}
