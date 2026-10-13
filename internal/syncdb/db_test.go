package syncdb

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMockDB(t *testing.T) (*DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	database, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}
	return database, mock
}

func TestGetUsersWithNotionKeysSkipsDisabled(t *testing.T) {
	database, mock := newMockDB(t)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "User" WHERE "notionKey" IS NOT NULL AND "notionKey" != '' AND disabled IS NOT TRUE`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	if _, err := database.GetUsersWithNotionKeys(context.Background()); err != nil {
		t.Fatalf("GetUsersWithNotionKeys: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestUpsertNoteFromNotionRejectsDisabledUser(t *testing.T) {
	database, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM "User" WHERE id = $1 AND disabled IS NOT TRUE FOR SHARE`)).
		WithArgs("alice").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	_, _, err := database.UpsertNoteFromNotion("alice", "uuid", "page", "c", nil, time.Now(), time.Now())
	if !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("err = %v, want ErrUserDisabled", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}
