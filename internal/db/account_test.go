package db

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
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

var deleteUserDataStatements = []string{
	`DELETE FROM "NoteTag" WHERE "noteId" IN (SELECT id FROM "Note" WHERE "userId" = $1)`,
	`DELETE FROM "NoteImage" WHERE "noteId" IN (SELECT id FROM "Note" WHERE "userId" = $1)`,
	`DELETE FROM "NoteAudio" WHERE "noteId" IN (SELECT id FROM "Note" WHERE "userId" = $1)`,
	`DELETE FROM "NoteTag" WHERE "tagId" IN (SELECT id FROM "Tag" WHERE "userId" = $1)`,
	`DELETE FROM "Note" WHERE "userId" = $1`,
	`DELETE FROM "Tag" WHERE "userId" = $1`,
	`DELETE FROM "ApiKey" WHERE "userId" = $1`,
	`DELETE FROM "SyncState" WHERE "userId" = $1`,
	`DELETE FROM "User" WHERE id = $1`,
}

func TestDeleteUserData(t *testing.T) {
	tests := []struct {
		name    string
		failAt  int
		wantErr bool
	}{
		{name: "deletes everything in one transaction", failAt: -1},
		{name: "rolls back when a delete fails", failAt: 4, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newMockDB(t)
			mock.ExpectBegin()
			for i, stmt := range deleteUserDataStatements {
				e := mock.ExpectExec(regexp.QuoteMeta(stmt)).WithArgs(testUserID)
				if i == tt.failAt {
					e.WillReturnError(errors.New("boom"))
					mock.ExpectRollback()
					break
				}
				e.WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if tt.failAt < 0 {
				mock.ExpectCommit()
			}

			err := database.DeleteUserData(context.Background(), testUserID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DeleteUserData err = %v, wantErr %v", err, tt.wantErr)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled expectations: %v", err)
			}
		})
	}
}

func TestUserMediaObjects(t *testing.T) {
	database, mock := newMockDB(t)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT "NoteImage"."gcsObjectName" FROM "NoteImage" JOIN "Note" ON "Note".id = "NoteImage"."noteId" WHERE "Note"."userId" = $1 UNION SELECT "NoteAudio"."gcsObjectName" FROM "NoteAudio" JOIN "Note" ON "Note".id = "NoteAudio"."noteId" WHERE "Note"."userId" = $2`)).
		WithArgs(testUserID, testUserID).
		WillReturnRows(sqlmock.NewRows([]string{colGCSObjectName}).
			AddRow("notes/n1/i1").AddRow("").AddRow("notes/n1/a1"))

	got, err := database.UserMediaObjects(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("UserMediaObjects: %v", err)
	}
	if diff := cmp.Diff([]string{"notes/n1/i1", "notes/n1/a1"}, got); diff != "" {
		t.Errorf("UserMediaObjects mismatch (-want +got):\n%s", diff)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestExportNotes(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	noteCols := []string{"id", colContent, colCreatedAt, colUpdatedAt, colUserID}

	tests := []struct {
		name      string
		cursor    *ExportCursor
		wantQuery string
		wantArgs  []driver.Value
		rows      *sqlmock.Rows
		wantIDs   []string
	}{
		{
			name:      "first page",
			wantQuery: `SELECT * FROM "Note" WHERE "userId" = $1 ORDER BY "createdAt" ASC, id ASC LIMIT $2`,
			wantArgs:  []driver.Value{testUserID, 2},
			rows:      sqlmock.NewRows(noteCols).AddRow("n1", "a", t1, t1, testUserID).AddRow("n2", "b", t2, t2, testUserID),
			wantIDs:   []string{"n1", "n2"},
		},
		{
			name:      "next page uses keyset cursor",
			cursor:    &ExportCursor{CreatedAt: t2, ID: "n2"},
			wantQuery: `SELECT * FROM "Note" WHERE "userId" = $1 AND ("createdAt", id) > ($2, $3) ORDER BY "createdAt" ASC, id ASC LIMIT $4`,
			wantArgs:  []driver.Value{testUserID, t2, "n2", 2},
			rows:      sqlmock.NewRows(noteCols),
			wantIDs:   []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newMockDB(t)
			mock.ExpectQuery(regexp.QuoteMeta(tt.wantQuery)).WithArgs(tt.wantArgs...).WillReturnRows(tt.rows)
			if len(tt.wantIDs) > 0 {
				mock.ExpectQuery(`FROM "Tag"`).WithArgs("n1", "n2").
					WillReturnRows(sqlmock.NewRows([]string{"note_id", "id", colName}).AddRow("n1", "t1", tagWork))
				mock.ExpectQuery(`FROM "NoteImage"`).WithArgs("n1", "n2").
					WillReturnRows(sqlmock.NewRows([]string{"id", colNoteID, colGCSObjectName}).AddRow("i1", "n2", "notes/n2/i1"))
				mock.ExpectQuery(`FROM "NoteAudio"`).WithArgs("n1", "n2").
					WillReturnRows(sqlmock.NewRows([]string{"id", colNoteID, colGCSObjectName}).AddRow("a1", "n1", "notes/n1/a1"))
			}

			notes, err := database.ExportNotes(context.Background(), testUserID, tt.cursor, 2)
			if err != nil {
				t.Fatalf("ExportNotes: %v", err)
			}
			gotIDs := []string{}
			for _, n := range notes {
				gotIDs = append(gotIDs, n.ID)
			}
			if diff := cmp.Diff(tt.wantIDs, gotIDs); diff != "" {
				t.Errorf("ids mismatch (-want +got):\n%s", diff)
			}
			if len(notes) == 2 {
				if len(notes[0].Tags) != 1 || notes[0].Tags[0].Name != tagWork {
					t.Errorf("note n1 tags = %+v", notes[0].Tags)
				}
				if len(notes[1].Images) != 1 || len(notes[0].Audios) != 1 {
					t.Errorf("media not attached: images=%+v audios=%+v", notes[1].Images, notes[0].Audios)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled expectations: %v", err)
			}
		})
	}
}

func TestFreezeUserForDeletion(t *testing.T) {
	tests := []struct {
		name      string
		revokeErr error
		wantErr   bool
	}{
		{name: "disables user and revokes keys in one transaction"},
		{name: "rolls back when key revocation fails", revokeErr: errors.New("boom"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newMockDB(t)
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta(`UPDATE "User" SET disabled = true, "disabledReason" = $1 WHERE id = $2`)).
				WithArgs(DeletionDisabledReason, testUserID).WillReturnResult(sqlmock.NewResult(0, 1))
			e := mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM "ApiKey" WHERE "userId" = $1`)).WithArgs(testUserID)
			if tt.revokeErr != nil {
				e.WillReturnError(tt.revokeErr)
				mock.ExpectRollback()
			} else {
				e.WillReturnResult(sqlmock.NewResult(0, 3))
				mock.ExpectCommit()
			}

			err := database.FreezeUserForDeletion(context.Background(), testUserID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("FreezeUserForDeletion err = %v, wantErr %v", err, tt.wantErr)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled expectations: %v", err)
			}
		})
	}
}
