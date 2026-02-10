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
	defer func() { _ = sqlDB.Close() }()

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
	defer func() { _ = sqlDB.Close() }()

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
	defer func() { _ = sqlDB.Close() }()

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
	defer func() { _ = sqlDB.Close() }()

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
	defer func() { _ = sqlDB.Close() }()

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
	defer func() { _ = sqlDB.Close() }()

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
	defer func() { _ = sqlDB.Close() }()

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
	defer func() { _ = sqlDB.Close() }()

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
	defer func() { _ = sqlDB.Close() }()

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

func TestCreateNote_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-create-note"

	// Transaction: BEGIN, INSERT Note, COMMIT
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "Note"`).
		WithArgs(
			sqlmock.AnyArg(), "hello", sqlmock.AnyArg(), sqlmock.AnyArg(), userID,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	// getNoteTags
	mock.ExpectQuery(`SELECT (.+) FROM "Tag"`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "createdAt", "userId"}))
	// getNoteImages
	mock.ExpectQuery(`SELECT (.+) FROM "NoteImage"`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "noteId", "url", "gcsObjectName", "extractedText", "mimeType", "createdAt"}))

	ctx := context.Background()
	note, err := db.CreateNote(ctx, userID, "hello", nil)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if note == nil {
		t.Fatal("CreateNote returned nil note")
	}
	if note.Content != "hello" || note.UserID != userID {
		t.Errorf("CreateNote: note = %+v", note)
	}

	// Override for expectations: CreateNote generates ID at runtime so we just check note ID is set
	if note.ID == "" {
		t.Error("CreateNote: note.ID is empty")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestUpdateNote_NotFound(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	content := "updated"
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT (.+) FROM "Note"`).
		WithArgs("note-missing", "user-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "content", "createdAt", "updatedAt", "userId", "externalId", "notionUuid", "lastSyncedToNotion"}))
	mock.ExpectCommit()

	ctx := context.Background()
	note, err := db.UpdateNote(ctx, "user-1", "note-missing", &content, nil, false)
	if err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	if note != nil {
		t.Errorf("UpdateNote: want nil when note not found, got %+v", note)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestAddImageToNote_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	noteID := "note-img"
	img := &NoteImage{
		ID:            "img-1",
		URL:           "https://example.com/img.png",
		GCSObjectName: "bucket/img.png",
		MimeType:      "image/png",
	}

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "NoteImage"`).
		WithArgs(sqlmock.AnyArg(), noteID, img.URL, img.GCSObjectName, sqlmock.AnyArg(), img.MimeType, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	err = db.AddImageToNote(ctx, noteID, img)
	if err != nil {
		t.Fatalf("AddImageToNote: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestRemoveImageFromNote_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID, noteID, imageID := "user-1", "note-1", "img-1"
	gcsName := "bucket/obj.png"
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT (.+) FROM "Note"`).
		WithArgs(noteID, userID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "content", "createdAt", "updatedAt", "userId", "externalId", "notionUuid", "lastSyncedToNotion"}).
			AddRow(noteID, "c", now, now, userID, nil, nil, nil))
	mock.ExpectQuery(`SELECT (.+) FROM "NoteImage"`).
		WithArgs(imageID, noteID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "noteId", "url", "gcsObjectName", "extractedText", "mimeType", "createdAt"}).
			AddRow(imageID, noteID, "https://u", gcsName, "", "image/png", now))
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "NoteImage"`).
		WithArgs(imageID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	got, err := db.RemoveImageFromNote(ctx, userID, noteID, imageID)
	if err != nil {
		t.Fatalf("RemoveImageFromNote: %v", err)
	}
	if got != gcsName {
		t.Errorf("RemoveImageFromNote: got GCS name %q, want %q", got, gcsName)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestRemoveImageFromNote_NoteNotFound(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	mock.ExpectQuery(`SELECT (.+) FROM "Note"`).
		WithArgs("note-missing", "user-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "content", "createdAt", "updatedAt", "userId"}))

	ctx := context.Background()
	_, err = db.RemoveImageFromNote(ctx, "user-1", "note-missing", "img-1")
	if err == nil || err.Error() != "note not found" {
		t.Errorf("RemoveImageFromNote: want 'note not found' error, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetNoteImages_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	noteID := "note-imgs"
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT (.+) FROM "NoteImage"`).
		WithArgs(noteID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "noteId", "url", "gcsObjectName", "extractedText", "mimeType", "createdAt"}).
			AddRow("img-1", noteID, "https://a", "gcs/a", "", "image/png", now))

	ctx := context.Background()
	images, err := db.GetNoteImages(ctx, noteID)
	if err != nil {
		t.Fatalf("GetNoteImages: %v", err)
	}
	if len(images) != 1 || images[0].ID != "img-1" {
		t.Errorf("GetNoteImages: got %+v", images)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetImagesByNoteID_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	noteID := "note-by-id"
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT (.+) FROM "NoteImage"`).
		WithArgs(noteID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "noteId", "url", "gcsObjectName", "extractedText", "mimeType", "createdAt"}).
			AddRow("i1", noteID, "u", "g", "", "", now))

	ctx := context.Background()
	images, err := db.GetImagesByNoteID(ctx, noteID)
	if err != nil {
		t.Fatalf("GetImagesByNoteID: %v", err)
	}
	if len(images) != 1 || images[0].ID != "i1" {
		t.Errorf("GetImagesByNoteID: got %+v", images)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestAddAudioToNote_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	noteID := "note-audio"
	audio := &NoteAudio{
		ID:            "aud-1",
		URL:           "https://example.com/a.mp3",
		GCSObjectName: "bucket/a.mp3",
		MimeType:      "audio/mpeg",
	}

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "NoteAudio"`).
		WithArgs(sqlmock.AnyArg(), noteID, audio.URL, audio.GCSObjectName, sqlmock.AnyArg(), audio.MimeType, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	err = db.AddAudioToNote(ctx, noteID, audio)
	if err != nil {
		t.Fatalf("AddAudioToNote: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestRemoveAudioFromNote_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID, noteID, audioID := "user-1", "note-1", "aud-1"
	gcsName := "bucket/audio.mp3"
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT (.+) FROM "Note"`).
		WithArgs(noteID, userID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "content", "createdAt", "updatedAt", "userId", "externalId", "notionUuid", "lastSyncedToNotion"}).
			AddRow(noteID, "c", now, now, userID, nil, nil, nil))
	mock.ExpectQuery(`SELECT (.+) FROM "NoteAudio"`).
		WithArgs(audioID, noteID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "noteId", "url", "gcsObjectName", "transcribedText", "mimeType", "createdAt"}).
			AddRow(audioID, noteID, "https://u", gcsName, "", "audio/mpeg", now))
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "NoteAudio"`).
		WithArgs(audioID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	got, err := db.RemoveAudioFromNote(ctx, userID, noteID, audioID)
	if err != nil {
		t.Fatalf("RemoveAudioFromNote: %v", err)
	}
	if got != gcsName {
		t.Errorf("RemoveAudioFromNote: got GCS name %q, want %q", got, gcsName)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestRemoveAudioFromNote_NoteNotFound(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	mock.ExpectQuery(`SELECT (.+) FROM "Note"`).
		WithArgs("note-missing", "user-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "content", "createdAt", "updatedAt", "userId"}))

	ctx := context.Background()
	_, err = db.RemoveAudioFromNote(ctx, "user-1", "note-missing", "aud-1")
	if err == nil || err.Error() != "note not found" {
		t.Errorf("RemoveAudioFromNote: want 'note not found' error, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetAudiosByNoteID_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	noteID := "note-audios"
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT (.+) FROM "NoteAudio"`).
		WithArgs(noteID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "noteId", "url", "gcsObjectName", "transcribedText", "mimeType", "createdAt"}).
			AddRow("a1", noteID, "u", "g", "", "", now))

	ctx := context.Background()
	audios, err := db.GetAudiosByNoteID(ctx, noteID)
	if err != nil {
		t.Fatalf("GetAudiosByNoteID: %v", err)
	}
	if len(audios) != 1 || audios[0].ID != "a1" {
		t.Errorf("GetAudiosByNoteID: got %+v", audios)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

// userRowColumns is the column set for scanning User in tests.
var userRowColumns = []string{
	"id", "email", "name", "image", "passwordHash", "subscriptionStatus",
	"subscriptionEnd", "createdAt", "stripeCustomerId", "notionKey", "updatedAt",
}

func TestCreateUser_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "User"`).
		WithArgs(
			sqlmock.AnyArg(), "new@example.com", sqlmock.AnyArg(), sqlmock.AnyArg(), "hashed", "free",
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	user, err := db.CreateUser(ctx, "new@example.com", "hashed")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user == nil {
		t.Fatal("CreateUser returned nil user")
	}
	if user.Email != "new@example.com" || user.PasswordHash != "hashed" || user.SubscriptionStatus != "free" {
		t.Errorf("CreateUser: user = %+v", user)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetUserByStripeCustomerID_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	stripeID := "cus_abc"
	userID := "user-stripe"
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT (.+) FROM "User" (.+)`).
		WithArgs(stripeID, 1).
		WillReturnRows(sqlmock.NewRows(userRowColumns).
			AddRow(userID, "u@example.com", nil, nil, "hash", "premium", nil, now, stripeID, nil, now))

	ctx := context.Background()
	user, err := db.GetUserByStripeCustomerID(ctx, stripeID)
	if err != nil {
		t.Fatalf("GetUserByStripeCustomerID: %v", err)
	}
	if user == nil || user.ID != userID {
		t.Errorf("GetUserByStripeCustomerID: user = %+v", user)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestUpdateUserSubscription_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-sub"
	stripeID := "cus_xyz"
	now := time.Now().UTC()

	mock.ExpectBegin()
	// UPDATE "User" SET stripeCustomerId=$1, subscriptionStatus=$2, updatedAt=$3 WHERE id=$4
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(stripeID, "premium", sqlmock.AnyArg(), userID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(`SELECT (.+) FROM "User"`).
		WithArgs(userID, 1).
		WillReturnRows(sqlmock.NewRows(userRowColumns).
			AddRow(userID, "u@example.com", nil, nil, "hash", "premium", nil, now, stripeID, nil, now))

	ctx := context.Background()
	stripeStr := stripeID
	user, err := db.UpdateUserSubscription(ctx, userID, "premium", &stripeStr, nil)
	if err != nil {
		t.Fatalf("UpdateUserSubscription: %v", err)
	}
	if user == nil || user.SubscriptionStatus != "premium" {
		t.Errorf("UpdateUserSubscription: user = %+v", user)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestCreateApiKey_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-apikey"
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "ApiKey"`).
		WithArgs(sqlmock.AnyArg(), "my key", "prefix", "hash", userID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	key, err := db.CreateApiKey(ctx, userID, "my key", "prefix", "hash")
	if err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if key == nil || key.Name != "my key" || key.KeyPrefix != "prefix" {
		t.Errorf("CreateApiKey: key = %+v", key)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestListApiKeys_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-keys"
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT (.+) FROM "ApiKey"`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "keyPrefix", "createdAt", "lastUsed", "userId"}).
			AddRow("key-1", "k1", "pre", now, nil, userID))

	ctx := context.Background()
	keys, err := db.ListApiKeys(ctx, userID)
	if err != nil {
		t.Fatalf("ListApiKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].Name != "k1" {
		t.Errorf("ListApiKeys: got %+v", keys)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestDeleteApiKey_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "ApiKey"`).
		WithArgs("key-1", "user-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	deleted, err := db.DeleteApiKey(ctx, "user-1", "key-1")
	if err != nil {
		t.Fatalf("DeleteApiKey: %v", err)
	}
	if !deleted {
		t.Error("DeleteApiKey: want true")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetApiKeysByPrefix_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT (.+) FROM "ApiKey"`).
		WithArgs("prefix_abc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "keyPrefix", "keyHash", "userId", "createdAt", "lastUsed"}).
			AddRow("key-1", "k", "prefix_abc", "hash", "user-1", now, nil))

	ctx := context.Background()
	keys, err := db.GetApiKeysByPrefix(ctx, "prefix_abc")
	if err != nil {
		t.Fatalf("GetApiKeysByPrefix: %v", err)
	}
	if len(keys) != 1 || keys[0].KeyPrefix != "prefix_abc" {
		t.Errorf("GetApiKeysByPrefix: got %+v", keys)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestUpdateApiKeyLastUsed_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "ApiKey"`).
		WithArgs(sqlmock.AnyArg(), "key-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	err = db.UpdateApiKeyLastUsed(ctx, "key-1")
	if err != nil {
		t.Fatalf("UpdateApiKeyLastUsed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetNotesWithFewTags_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-few"
	noteID := "note-1"
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT (.+) FROM "Note"`).
		WithArgs(userID, 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "content", "createdAt", "updatedAt", "userId", "externalId", "notionUuid", "lastSyncedToNotion"}).
			AddRow(noteID, "content", now, now, userID, nil, nil, nil))
	mock.ExpectQuery(`SELECT (.+) FROM "Tag"`).
		WithArgs(noteID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "createdAt", "userId"}))

	ctx := context.Background()
	notes, err := db.GetNotesWithFewTags(ctx, userID, 2)
	if err != nil {
		t.Fatalf("GetNotesWithFewTags: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != noteID {
		t.Errorf("GetNotesWithFewTags: got %+v", notes)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestAddTagsToNote_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID, noteID := "user-1", "note-1"
	now := time.Now().UTC()

	// Transaction: BEGIN, SELECT note, SELECT tag (not found), INSERT tag, SELECT NoteTag (not found), INSERT NoteTag, COMMIT
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT (.+) FROM "Note"`).
		WithArgs(noteID, userID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "content", "createdAt", "updatedAt", "userId", "externalId", "notionUuid", "lastSyncedToNotion"}).
			AddRow(noteID, "c", now, now, userID, nil, nil, nil))
	mock.ExpectQuery(`SELECT (.+) FROM "Tag"`).
		WithArgs(userID, "work", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "createdAt", "userId"}))
	mock.ExpectExec(`INSERT INTO "Tag"`).
		WithArgs(sqlmock.AnyArg(), "work", sqlmock.AnyArg(), userID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT (.+) FROM "NoteTag"`).
		WithArgs(noteID, sqlmock.AnyArg(), 1).
		WillReturnRows(sqlmock.NewRows([]string{"noteId", "tagId"}))
	mock.ExpectExec(`INSERT INTO "NoteTag"`).
		WithArgs(noteID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "Note"`).
		WithArgs(sqlmock.AnyArg(), noteID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	err = db.AddTagsToNote(ctx, userID, noteID, []string{"work"})
	if err != nil {
		t.Fatalf("AddTagsToNote: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetUserSettings_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-settings"
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT (.+) FROM "User"`).
		WithArgs(userID, 1).
		WillReturnRows(sqlmock.NewRows(userRowColumns).
			AddRow(userID, "u@ex.com", nil, nil, "hash", "free", nil, now, nil, nil, now))

	ctx := context.Background()
	user, err := db.GetUserSettings(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if user == nil || user.ID != userID {
		t.Errorf("GetUserSettings: user = %+v", user)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestUpdateUserSettings_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-up"
	now := time.Now().UTC()
	name := "New Name"

	mock.ExpectQuery(`SELECT (.+) FROM "User"`).
		WithArgs(userID, 1).
		WillReturnRows(sqlmock.NewRows(userRowColumns).
			AddRow(userID, "u@ex.com", nil, nil, "hash", "free", nil, now, nil, nil, now))
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs("New Name", sqlmock.AnyArg(), userID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	// Reload user: WHERE id = $1 AND "User"."id" = $2 ORDER BY ... LIMIT $3
	mock.ExpectQuery(`SELECT (.+) FROM "User"`).
		WithArgs(userID, userID, 1).
		WillReturnRows(sqlmock.NewRows(userRowColumns).
			AddRow(userID, "u@ex.com", &name, nil, "hash", "free", nil, now, nil, nil, now))

	ctx := context.Background()
	user, err := db.UpdateUserSettings(ctx, userID, nil, &name, nil, nil)
	if err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	if user == nil || user.Name == nil || *user.Name != "New Name" {
		t.Errorf("UpdateUserSettings: user = %+v", user)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetUsersWithNotionKeys_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT (.+) FROM "User"`).
		WillReturnRows(sqlmock.NewRows(userRowColumns).
			AddRow("u1", "a@b.com", nil, nil, "h", "free", nil, now, nil, "notion-key", now))

	ctx := context.Background()
	users, err := db.GetUsersWithNotionKeys(ctx)
	if err != nil {
		t.Fatalf("GetUsersWithNotionKeys: %v", err)
	}
	if len(users) != 1 || users[0].NotionKey == nil || *users[0].NotionKey != "notion-key" {
		t.Errorf("GetUsersWithNotionKeys: got %+v", users)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestListAllUsers_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT (.+) FROM "User"`).
		WillReturnRows(sqlmock.NewRows(userRowColumns).
			AddRow("u1", "a@b.com", nil, nil, "h", "free", nil, now, nil, nil, now).
			AddRow("u2", "b@b.com", nil, nil, "h", "free", nil, now, nil, nil, now))

	ctx := context.Background()
	users, err := db.ListAllUsers(ctx)
	if err != nil {
		t.Fatalf("ListAllUsers: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("ListAllUsers: got %d users, want 2", len(users))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetRandomNotes_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-rand"
	noteID := "note-1"
	now := time.Now().UTC()

	mock.ExpectQuery(`SELECT (.+) FROM "Note"`).
		WithArgs(userID, 5).
		WillReturnRows(sqlmock.NewRows([]string{"id", "content", "createdAt", "updatedAt", "userId", "externalId", "notionUuid", "lastSyncedToNotion"}).
			AddRow(noteID, "c", now, now, userID, nil, nil, nil))
	mock.ExpectQuery(`SELECT (.+) FROM "Tag"`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"note_id", "id", "name", "createdAt", "userId"}))
	mock.ExpectQuery(`SELECT (.+) FROM "NoteImage"`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "noteId", "url", "gcsObjectName", "extractedText", "mimeType", "createdAt"}))

	ctx := context.Background()
	notes, err := db.GetRandomNotes(ctx, userID, 5)
	if err != nil {
		t.Fatalf("GetRandomNotes: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != noteID {
		t.Errorf("GetRandomNotes: got %+v", notes)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetStats_SQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	userID := "user-stats"

	mock.ExpectQuery(`SELECT count\(.+\) FROM "Note"`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))
	mock.ExpectQuery(`SELECT count\(.+\) FROM "Tag"`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))
	mock.ExpectQuery(`SELECT (.+) FROM "Note"`).
		WithArgs(userID, 1000).
		WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow("one two three"))

	ctx := context.Background()
	blips, tags, words, err := db.GetStats(ctx, userID)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if blips != 42 || tags != 10 || words != 3 {
		t.Errorf("GetStats: got blips=%d tags=%d words=%d", blips, tags, words)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}
