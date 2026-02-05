package db

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
)

func TestNewFromConn(t *testing.T) {
	sqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer sqlDB.Close()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}
	if db == nil {
		t.Fatal("NewFromConn returned nil DB")
	}
}

func TestGetUser_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer sqlDB.Close()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-123"
	now := time.Now().UTC()

	// GORM generates: SELECT * FROM "User" WHERE id = $1 ORDER BY "User"."id" LIMIT $2
	mock.ExpectQuery(`SELECT (.+) FROM "User" (.+)`).
		WithArgs(userID, 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "email", "name", "image", "passwordHash", "subscriptionStatus",
			"subscriptionEnd", "createdAt", "stripeCustomerId", "notionKey", "updatedAt",
		}).AddRow(
			userID, "test@example.com", nil, nil, "hash", "free",
			nil, now, nil, nil, now,
		))

	ctx := context.Background()
	user, err := db.GetUser(ctx, userID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user == nil {
		t.Fatal("GetUser returned nil user")
	}
	if user.ID != userID {
		t.Errorf("user.ID = %q, want %q", user.ID, userID)
	}
	if user.Email != "test@example.com" {
		t.Errorf("user.Email = %q, want test@example.com", user.Email)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetUser_NotFound(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer sqlDB.Close()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	// Return empty rows so GORM sets ErrRecordNotFound and GetUser returns (nil, nil)
	mock.ExpectQuery(`SELECT (.+) FROM "User" (.+)`).
		WithArgs("nonexistent", 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "email", "name", "image", "passwordHash", "subscriptionStatus",
			"subscriptionEnd", "createdAt", "stripeCustomerId", "notionKey", "updatedAt",
		}))

	ctx := context.Background()
	user, err := db.GetUser(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user != nil {
		t.Errorf("GetUser want nil, got user %+v", user)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetUserByEmail_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer sqlDB.Close()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-456"
	email := "byemail@example.com"
	now := time.Now().UTC()

	// GORM: SELECT * FROM "User" WHERE email = $1 ORDER BY "User"."id" LIMIT $2
	mock.ExpectQuery(`SELECT (.+) FROM "User" (.+)`).
		WithArgs(email, 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "email", "name", "image", "passwordHash", "subscriptionStatus",
			"subscriptionEnd", "createdAt", "stripeCustomerId", "notionKey", "updatedAt",
		}).AddRow(
			userID, email, nil, nil, "hash", "free",
			nil, now, nil, nil, now,
		))

	ctx := context.Background()
	user, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if user == nil {
		t.Fatal("GetUserByEmail returned nil user")
	}
	if user.Email != email {
		t.Errorf("user.Email = %q, want %q", user.Email, email)
	}
	if user.ID != userID {
		t.Errorf("user.ID = %q, want %q", user.ID, userID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestDeleteNote_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer sqlDB.Close()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-1"
	noteID := "note-1"

	// GORM may run in a transaction; postgres driver can trigger Begin.
	// DELETE FROM "Note" WHERE id = $1 AND "userId" = $2
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "Note"`).
		WithArgs(noteID, userID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	deleted, err := db.DeleteNote(ctx, userID, noteID)
	if err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if !deleted {
		t.Error("DeleteNote: want true, got false")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestDeleteNote_NotFound(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer sqlDB.Close()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "Note"`).
		WithArgs("note-missing", "user-1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	ctx := context.Background()
	deleted, err := db.DeleteNote(ctx, "user-1", "note-missing")
	if err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if deleted {
		t.Error("DeleteNote: want false when no rows affected")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestListTags_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer sqlDB.Close()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-tags"
	now := time.Now().UTC()

	// ListTags: SELECT "Tag".*, COUNT("NoteTag"."noteId") ... LEFT JOIN "NoteTag" ... WHERE "Tag"."userId" = $1 GROUP BY "Tag".id ORDER BY "Tag".name
	mock.ExpectQuery(`SELECT (.+) FROM "Tag"`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "createdAt", "userId", "count",
		}).AddRow("tag-1", "work", now, userID, 3).AddRow("tag-2", "personal", now, userID, 1))

	ctx := context.Background()
	tags, err := db.ListTags(ctx, userID)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("ListTags: got %d tags, want 2", len(tags))
	}
	if tags[0].Name != "work" {
		t.Errorf("tags[0].Name = %q, want work", tags[0].Name)
	}
	if tags[1].Name != "personal" {
		t.Errorf("tags[1].Name = %q, want personal", tags[1].Name)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetNote_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer sqlDB.Close()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-note"
	noteID := "note-abc"
	now := time.Now().UTC()

	// 1) GetNote: SELECT * FROM "Note" WHERE id = $1 AND "userId" = $2 ORDER BY ... LIMIT $3
	mock.ExpectQuery(`SELECT (.+) FROM "Note" (.+)`).
		WithArgs(noteID, userID, 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "content", "createdAt", "updatedAt", "userId",
			"externalId", "notionUuid", "lastSyncedToNotion",
		}).AddRow(noteID, "hello world", now, now, userID, nil, nil, nil))

	// 2) getNoteTags: JOIN Tag with NoteTag WHERE noteId = $1
	mock.ExpectQuery(`SELECT (.+) FROM "Tag"`).
		WithArgs(noteID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "createdAt", "userId"}).
			AddRow("tag-1", "work", now, userID))

	// 3) getNoteImages: SELECT * FROM "NoteImage" WHERE "noteId" = $1
	mock.ExpectQuery(`SELECT (.+) FROM "NoteImage"`).
		WithArgs(noteID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "noteId", "url", "gcsObjectName", "extractedText", "mimeType", "createdAt",
		}))

	ctx := context.Background()
	note, err := db.GetNote(ctx, userID, noteID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if note == nil {
		t.Fatal("GetNote returned nil note")
	}
	if note.ID != noteID || note.Content != "hello world" {
		t.Errorf("note = %+v", note)
	}
	if len(note.Tags) != 1 || note.Tags[0].Name != "work" {
		t.Errorf("note.Tags = %+v", note.Tags)
	}
	if len(note.Images) != 0 {
		t.Errorf("note.Images = %+v", note.Images)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestListNotes_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer sqlDB.Close()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-list"
	noteID := "note-1"
	now := time.Now().UTC()

	// 1) Count: SELECT count(*) FROM "Note" WHERE "userId" = $1
	mock.ExpectQuery(`SELECT count\(.+\) FROM "Note"`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// 2) Find: SELECT * FROM "Note" WHERE "userId" = $1 ORDER BY "createdAt" DESC LIMIT $2 (offset 0 may be in SQL)
	mock.ExpectQuery(`SELECT (.+) FROM "Note"`).
		WithArgs(userID, 10).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "content", "createdAt", "updatedAt", "userId",
			"externalId", "notionUuid", "lastSyncedToNotion",
		}).AddRow(noteID, "content", now, now, userID, nil, nil, nil))

	// 3) getTagsForNotes: batch fetch tags for note IDs
	mock.ExpectQuery(`SELECT (.+) FROM "Tag"`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"note_id", "id", "name", "createdAt", "userId"}).
			AddRow(noteID, "tag-1", "work", now, userID))

	// 4) getImagesForNotes: batch fetch images
	mock.ExpectQuery(`SELECT (.+) FROM "NoteImage"`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "noteId", "url", "gcsObjectName", "extractedText", "mimeType", "createdAt",
		}))

	ctx := context.Background()
	notes, total, err := db.ListNotes(ctx, userID, "", nil, "", "", 10, 0)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(notes) != 1 {
		t.Fatalf("len(notes) = %d, want 1", len(notes))
	}
	if notes[0].ID != noteID {
		t.Errorf("notes[0].ID = %q, want %q", notes[0].ID, noteID)
	}
	if diff := cmp.Diff(notes[0].Tags, []Tag{{ID: "tag-1", Name: "work", CreatedAt: now, UserID: userID}}); diff != "" {
		t.Errorf("notes[0].Tags mismatch (-got +want):\n%s", diff)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}
