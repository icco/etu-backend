package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// DB wraps the database connection
type DB struct {
	conn *sql.DB
}

// Note represents a note in the database
type Note struct {
	ID        string
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
	UserID    string
	Tags      []string
}

// Tag represents a tag in the database
type Tag struct {
	ID        string
	Name      string
	CreatedAt time.Time
	UserID    string
	Count     int
}

// User represents a user in the database
type User struct {
	ID                 string
	Email              string
	Name               *string
	Image              *string
	PasswordHash       string
	SubscriptionStatus string
	SubscriptionEnd    *time.Time
	CreatedAt          time.Time
	StripeCustomerID   *string
}

// ApiKey represents an API key in the database
type ApiKey struct {
	ID        string
	Name      string
	KeyPrefix string
	KeyHash   string
	UserID    string
	CreatedAt time.Time
	LastUsed  *time.Time
}

// New creates a new database connection
func New() (*DB, error) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable not set")
	}

	conn, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Test the connection
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Set connection pool settings
	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(5 * time.Minute)

	return &DB{conn: conn}, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// ListNotes retrieves notes for a user with optional filtering
func (db *DB) ListNotes(ctx context.Context, userID, search string, tags []string, startDate, endDate string, limit, offset int) ([]Note, int, error) {
	// Build the query dynamically
	baseQuery := `
		SELECT DISTINCT n.id, n.content, n."createdAt", n."updatedAt", n."userId"
		FROM "Note" n
	`
	countQuery := `
		SELECT COUNT(DISTINCT n.id)
		FROM "Note" n
	`

	var conditions []string
	var args []interface{}
	argNum := 1

	conditions = append(conditions, fmt.Sprintf(`n."userId" = $%d`, argNum))
	args = append(args, userID)
	argNum++

	// Join for tag filtering if tags are specified
	if len(tags) > 0 {
		baseQuery += `
			LEFT JOIN "NoteTag" nt ON n.id = nt."noteId"
			LEFT JOIN "Tag" t ON nt."tagId" = t.id
		`
		countQuery += `
			LEFT JOIN "NoteTag" nt ON n.id = nt."noteId"
			LEFT JOIN "Tag" t ON nt."tagId" = t.id
		`
		placeholders := make([]string, len(tags))
		for i, tag := range tags {
			placeholders[i] = fmt.Sprintf("$%d", argNum)
			args = append(args, tag)
			argNum++
		}
		conditions = append(conditions, fmt.Sprintf("t.name IN (%s)", strings.Join(placeholders, ", ")))
	}

	// Search filter
	if search != "" {
		conditions = append(conditions, fmt.Sprintf("n.content ILIKE $%d", argNum))
		args = append(args, "%"+search+"%")
		argNum++
	}

	// Date filters
	if startDate != "" {
		conditions = append(conditions, fmt.Sprintf(`n."createdAt" >= $%d`, argNum))
		args = append(args, startDate)
		argNum++
	}
	if endDate != "" {
		conditions = append(conditions, fmt.Sprintf(`n."createdAt" <= $%d`, argNum))
		args = append(args, endDate)
		argNum++
	}

	whereClause := " WHERE " + strings.Join(conditions, " AND ")

	// Get total count
	var total int
	err := db.conn.QueryRowContext(ctx, countQuery+whereClause, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count notes: %w", err)
	}

	// Apply ordering, limit and offset
	fullQuery := baseQuery + whereClause + fmt.Sprintf(` ORDER BY n."createdAt" DESC LIMIT $%d OFFSET $%d`, argNum, argNum+1)
	args = append(args, limit, offset)

	rows, err := db.conn.QueryContext(ctx, fullQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query notes: %w", err)
	}
	defer rows.Close()

	var notes []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Content, &n.CreatedAt, &n.UpdatedAt, &n.UserID); err != nil {
			return nil, 0, fmt.Errorf("failed to scan note: %w", err)
		}
		notes = append(notes, n)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating notes: %w", err)
	}

	// Fetch tags for each note
	for i := range notes {
		tags, err := db.getNoteTags(ctx, notes[i].ID)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get tags for note %s: %w", notes[i].ID, err)
		}
		notes[i].Tags = tags
	}

	return notes, total, nil
}

// getNoteTags retrieves tag names for a note
func (db *DB) getNoteTags(ctx context.Context, noteID string) ([]string, error) {
	query := `
		SELECT t.name
		FROM "Tag" t
		JOIN "NoteTag" nt ON t.id = nt."tagId"
		WHERE nt."noteId" = $1
		ORDER BY t.name
	`

	rows, err := db.conn.QueryContext(ctx, query, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}

	return tags, rows.Err()
}

// GetNote retrieves a single note by ID for a user
func (db *DB) GetNote(ctx context.Context, userID, noteID string) (*Note, error) {
	query := `
		SELECT id, content, "createdAt", "updatedAt", "userId"
		FROM "Note"
		WHERE id = $1 AND "userId" = $2
	`

	var n Note
	err := db.conn.QueryRowContext(ctx, query, noteID, userID).Scan(&n.ID, &n.Content, &n.CreatedAt, &n.UpdatedAt, &n.UserID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get note: %w", err)
	}

	tags, err := db.getNoteTags(ctx, n.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tags for note: %w", err)
	}
	n.Tags = tags

	return &n, nil
}

// CreateNote creates a new note with optional tags
func (db *DB) CreateNote(ctx context.Context, userID, content string, tagNames []string) (*Note, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			log.Printf("failed to rollback transaction: %v", err)
		}
	}()

	// Generate CUID-like ID
	noteID := generateCUID()
	now := time.Now()

	// Insert note
	_, err = tx.ExecContext(ctx, `
		INSERT INTO "Note" (id, content, "createdAt", "updatedAt", "userId")
		VALUES ($1, $2, $3, $4, $5)
	`, noteID, content, now, now, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to insert note: %w", err)
	}

	// Create tags and link them
	for _, tagName := range tagNames {
		if tagName == "" {
			continue
		}

		// Upsert tag
		tagID := generateCUID()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO "Tag" (id, name, "createdAt", "userId")
			VALUES ($1, $2, $3, $4)
			ON CONFLICT ("userId", name) DO NOTHING
		`, tagID, tagName, now, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert tag: %w", err)
		}

		// Get the tag ID (either new or existing)
		var actualTagID string
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM "Tag" WHERE "userId" = $1 AND name = $2
		`, userID, tagName).Scan(&actualTagID)
		if err != nil {
			return nil, fmt.Errorf("failed to get tag ID: %w", err)
		}

		// Link note to tag
		_, err = tx.ExecContext(ctx, `
			INSERT INTO "NoteTag" ("noteId", "tagId")
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, noteID, actualTagID)
		if err != nil {
			return nil, fmt.Errorf("failed to link note to tag: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &Note{
		ID:        noteID,
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
		UserID:    userID,
		Tags:      tagNames,
	}, nil
}

// UpdateNote updates an existing note
func (db *DB) UpdateNote(ctx context.Context, userID, noteID string, content *string, tagNames []string, updateTags bool) (*Note, error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			log.Printf("failed to rollback transaction: %v", err)
		}
	}()

	// Verify ownership and get current note
	var existingContent string
	var createdAt time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT content, "createdAt" FROM "Note" WHERE id = $1 AND "userId" = $2
	`, noteID, userID).Scan(&existingContent, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to verify note ownership: %w", err)
	}

	now := time.Now()
	newContent := existingContent
	if content != nil {
		newContent = *content
	}

	// Update note
	_, err = tx.ExecContext(ctx, `
		UPDATE "Note" SET content = $1, "updatedAt" = $2 WHERE id = $3
	`, newContent, now, noteID)
	if err != nil {
		return nil, fmt.Errorf("failed to update note: %w", err)
	}

	// Update tags if requested
	var finalTags []string
	if updateTags {
		// Remove existing tag links
		_, err = tx.ExecContext(ctx, `DELETE FROM "NoteTag" WHERE "noteId" = $1`, noteID)
		if err != nil {
			return nil, fmt.Errorf("failed to remove tag links: %w", err)
		}

		// Add new tags
		for _, tagName := range tagNames {
			if tagName == "" {
				continue
			}

			// Upsert tag
			tagID := generateCUID()
			_, err = tx.ExecContext(ctx, `
				INSERT INTO "Tag" (id, name, "createdAt", "userId")
				VALUES ($1, $2, $3, $4)
				ON CONFLICT ("userId", name) DO NOTHING
			`, tagID, tagName, now, userID)
			if err != nil {
				return nil, fmt.Errorf("failed to upsert tag: %w", err)
			}

			// Get the tag ID
			var actualTagID string
			err = tx.QueryRowContext(ctx, `
				SELECT id FROM "Tag" WHERE "userId" = $1 AND name = $2
			`, userID, tagName).Scan(&actualTagID)
			if err != nil {
				return nil, fmt.Errorf("failed to get tag ID: %w", err)
			}

			// Link note to tag
			_, err = tx.ExecContext(ctx, `
				INSERT INTO "NoteTag" ("noteId", "tagId")
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, noteID, actualTagID)
			if err != nil {
				return nil, fmt.Errorf("failed to link note to tag: %w", err)
			}
		}
		finalTags = tagNames
	} else {
		// Get existing tags
		finalTags, err = db.getNoteTags(ctx, noteID)
		if err != nil {
			return nil, fmt.Errorf("failed to get existing tags: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &Note{
		ID:        noteID,
		Content:   newContent,
		CreatedAt: createdAt,
		UpdatedAt: now,
		UserID:    userID,
		Tags:      finalTags,
	}, nil
}

// DeleteNote deletes a note by ID for a user
func (db *DB) DeleteNote(ctx context.Context, userID, noteID string) (bool, error) {
	result, err := db.conn.ExecContext(ctx, `
		DELETE FROM "Note" WHERE id = $1 AND "userId" = $2
	`, noteID, userID)
	if err != nil {
		return false, fmt.Errorf("failed to delete note: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected > 0, nil
}

// ListTags retrieves all tags for a user with usage counts
func (db *DB) ListTags(ctx context.Context, userID string) ([]Tag, error) {
	query := `
		SELECT t.id, t.name, t."createdAt", t."userId", COUNT(nt."noteId") as count
		FROM "Tag" t
		LEFT JOIN "NoteTag" nt ON t.id = nt."tagId"
		WHERE t."userId" = $1
		GROUP BY t.id, t.name, t."createdAt", t."userId"
		ORDER BY t.name
	`

	rows, err := db.conn.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query tags: %w", err)
	}
	defer rows.Close()

	var tags []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt, &t.UserID, &t.Count); err != nil {
			return nil, fmt.Errorf("failed to scan tag: %w", err)
		}
		tags = append(tags, t)
	}

	return tags, rows.Err()
}

// generateCUID generates a CUID-like identifier
// This is a simplified version - in production you might use a proper CUID library
func generateCUID() string {
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	result := make([]byte, 25)
	result[0] = 'c'

	timestamp := time.Now().UnixMilli()
	for i := 1; i < 9; i++ {
		result[i] = chars[timestamp%36]
		timestamp /= 36
	}

	for i := 9; i < 25; i++ {
		result[i] = chars[time.Now().UnixNano()%36]
	}

	return string(result)
}

// CreateUser creates a new user with email and password
func (db *DB) CreateUser(ctx context.Context, email, passwordHash string) (*User, error) {
	userID := generateCUID()
	now := time.Now()

	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO "User" (id, email, "passwordHash", "subscriptionStatus", "createdAt")
		VALUES ($1, $2, $3, $4, $5)
	`, userID, email, passwordHash, "free", now)
	if err != nil {
		return nil, fmt.Errorf("failed to insert user: %w", err)
	}

	return &User{
		ID:                 userID,
		Email:              email,
		PasswordHash:       passwordHash,
		SubscriptionStatus: "free",
		CreatedAt:          now,
	}, nil
}

// GetUserByEmail retrieves a user by email address
func (db *DB) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	query := `
		SELECT id, email, name, image, "passwordHash", "subscriptionStatus", "subscriptionEnd", "createdAt", "stripeCustomerId"
		FROM "User"
		WHERE email = $1
	`

	var u User
	var subscriptionEnd sql.NullTime
	var stripeCustomerID sql.NullString
	var name, image sql.NullString

	err := db.conn.QueryRowContext(ctx, query, email).Scan(
		&u.ID, &u.Email, &name, &image, &u.PasswordHash,
		&u.SubscriptionStatus, &subscriptionEnd, &u.CreatedAt, &stripeCustomerID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if name.Valid {
		u.Name = &name.String
	}
	if image.Valid {
		u.Image = &image.String
	}
	if subscriptionEnd.Valid {
		u.SubscriptionEnd = &subscriptionEnd.Time
	}
	if stripeCustomerID.Valid {
		u.StripeCustomerID = &stripeCustomerID.String
	}

	return &u, nil
}

// GetUser retrieves a user by ID
func (db *DB) GetUser(ctx context.Context, userID string) (*User, error) {
	query := `
		SELECT id, email, name, image, "passwordHash", "subscriptionStatus", "subscriptionEnd", "createdAt", "stripeCustomerId"
		FROM "User"
		WHERE id = $1
	`

	var u User
	var subscriptionEnd sql.NullTime
	var stripeCustomerID sql.NullString
	var name, image sql.NullString

	err := db.conn.QueryRowContext(ctx, query, userID).Scan(
		&u.ID, &u.Email, &name, &image, &u.PasswordHash,
		&u.SubscriptionStatus, &subscriptionEnd, &u.CreatedAt, &stripeCustomerID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if name.Valid {
		u.Name = &name.String
	}
	if image.Valid {
		u.Image = &image.String
	}
	if subscriptionEnd.Valid {
		u.SubscriptionEnd = &subscriptionEnd.Time
	}
	if stripeCustomerID.Valid {
		u.StripeCustomerID = &stripeCustomerID.String
	}

	return &u, nil
}

// GetUserByStripeCustomerID retrieves a user by Stripe customer ID
func (db *DB) GetUserByStripeCustomerID(ctx context.Context, stripeCustomerID string) (*User, error) {
	query := `
		SELECT id, email, name, image, "passwordHash", "subscriptionStatus", "subscriptionEnd", "createdAt", "stripeCustomerId"
		FROM "User"
		WHERE "stripeCustomerId" = $1
	`

	var u User
	var subscriptionEnd sql.NullTime
	var stripeID sql.NullString
	var name, image sql.NullString

	err := db.conn.QueryRowContext(ctx, query, stripeCustomerID).Scan(
		&u.ID, &u.Email, &name, &image, &u.PasswordHash,
		&u.SubscriptionStatus, &subscriptionEnd, &u.CreatedAt, &stripeID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if name.Valid {
		u.Name = &name.String
	}
	if image.Valid {
		u.Image = &image.String
	}
	if subscriptionEnd.Valid {
		u.SubscriptionEnd = &subscriptionEnd.Time
	}
	if stripeID.Valid {
		u.StripeCustomerID = &stripeID.String
	}

	return &u, nil
}

// UpdateUserSubscription updates a user's subscription information
func (db *DB) UpdateUserSubscription(ctx context.Context, userID, subscriptionStatus string, stripeCustomerID *string, subscriptionEnd *time.Time) (*User, error) {
	// Build dynamic update query
	query := `UPDATE "User" SET "subscriptionStatus" = $1`
	args := []interface{}{subscriptionStatus}
	argNum := 2

	if stripeCustomerID != nil {
		query += fmt.Sprintf(`, "stripeCustomerId" = $%d`, argNum)
		args = append(args, *stripeCustomerID)
		argNum++
	}

	if subscriptionEnd != nil {
		query += fmt.Sprintf(`, "subscriptionEnd" = $%d`, argNum)
		args = append(args, *subscriptionEnd)
		argNum++
	}

	query += fmt.Sprintf(` WHERE id = $%d`, argNum)
	args = append(args, userID)

	result, err := db.conn.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update user subscription: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return nil, nil
	}

	return db.GetUser(ctx, userID)
}

// CreateApiKey creates a new API key for a user
func (db *DB) CreateApiKey(ctx context.Context, userID, name, keyPrefix, keyHash string) (*ApiKey, error) {
	keyID := generateCUID()
	now := time.Now()

	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO "ApiKey" (id, name, "keyPrefix", "keyHash", "userId", "createdAt")
		VALUES ($1, $2, $3, $4, $5, $6)
	`, keyID, name, keyPrefix, keyHash, userID, now)
	if err != nil {
		return nil, fmt.Errorf("failed to insert API key: %w", err)
	}

	return &ApiKey{
		ID:        keyID,
		Name:      name,
		KeyPrefix: keyPrefix,
		KeyHash:   keyHash,
		UserID:    userID,
		CreatedAt: now,
	}, nil
}

// ListApiKeys retrieves all API keys for a user (without the hash)
func (db *DB) ListApiKeys(ctx context.Context, userID string) ([]ApiKey, error) {
	query := `
		SELECT id, name, "keyPrefix", "createdAt", "lastUsed"
		FROM "ApiKey"
		WHERE "userId" = $1
		ORDER BY "createdAt" DESC
	`

	rows, err := db.conn.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query API keys: %w", err)
	}
	defer rows.Close()

	var keys []ApiKey
	for rows.Next() {
		var k ApiKey
		var lastUsed sql.NullTime
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.CreatedAt, &lastUsed); err != nil {
			return nil, fmt.Errorf("failed to scan API key: %w", err)
		}
		if lastUsed.Valid {
			k.LastUsed = &lastUsed.Time
		}
		k.UserID = userID
		keys = append(keys, k)
	}

	return keys, rows.Err()
}

// DeleteApiKey deletes an API key for a user
func (db *DB) DeleteApiKey(ctx context.Context, userID, keyID string) (bool, error) {
	result, err := db.conn.ExecContext(ctx, `
		DELETE FROM "ApiKey" WHERE id = $1 AND "userId" = $2
	`, keyID, userID)
	if err != nil {
		return false, fmt.Errorf("failed to delete API key: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected > 0, nil
}

// GetApiKeysByPrefix retrieves API keys by prefix for verification
func (db *DB) GetApiKeysByPrefix(ctx context.Context, keyPrefix string) ([]ApiKey, error) {
	query := `
		SELECT id, name, "keyPrefix", "keyHash", "userId", "createdAt", "lastUsed"
		FROM "ApiKey"
		WHERE "keyPrefix" = $1
	`

	rows, err := db.conn.QueryContext(ctx, query, keyPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to query API keys: %w", err)
	}
	defer rows.Close()

	var keys []ApiKey
	for rows.Next() {
		var k ApiKey
		var lastUsed sql.NullTime
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.KeyHash, &k.UserID, &k.CreatedAt, &lastUsed); err != nil {
			return nil, fmt.Errorf("failed to scan API key: %w", err)
		}
		if lastUsed.Valid {
			k.LastUsed = &lastUsed.Time
		}
		keys = append(keys, k)
	}

	return keys, rows.Err()
}

// UpdateApiKeyLastUsed updates the lastUsed timestamp for an API key
func (db *DB) UpdateApiKeyLastUsed(ctx context.Context, keyID string) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE "ApiKey" SET "lastUsed" = $1 WHERE id = $2
	`, time.Now(), keyID)
	return err
}
