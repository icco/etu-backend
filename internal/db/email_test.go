package db

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestNormalizeEmail(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"alice@example.com", "alice@example.com"},
		{" Alice@Example.COM ", "alice@example.com"},
		{"", ""},
	} {
		if diff := cmp.Diff(tc.want, NormalizeEmail(tc.in)); diff != "" {
			t.Errorf("NormalizeEmail(%q) (-want +got):\n%s", tc.in, diff)
		}
	}
}

func TestGetUserByEmail_CaseInsensitive(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	database, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`SELECT \* FROM "User" WHERE lower\(email\) = \$1`).
		WithArgs("alice@example.com", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", colEmail, colCreatedAt}).AddRow("u1", "Alice@Example.com", time.Now()))
	user, err := database.GetUserByEmail(context.Background(), " ALICE@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if user == nil || user.ID != "u1" {
		t.Fatalf("user = %+v", user)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateUser_NormalizesEmail(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	database, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	args := []driver.Value{sqlmock.AnyArg(), "bob@example.com"}
	for range 24 {
		args = append(args, sqlmock.AnyArg())
	}
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "User"`).WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	user, err := database.CreateUser(context.Background(), " Bob@Example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("bob@example.com", user.Email); diff != "" {
		t.Errorf("email (-want +got):\n%s", diff)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureEmailIndex(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dupes   int
		wantErr string
	}{
		{name: "creates index when unique"},
		{name: "fails loudly on duplicates", dupes: 2, wantErr: "2 email addresses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sqlDB.Close() }()
			database, err := NewFromConn(sqlDB)
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectQuery(`SELECT count\(\*\) FROM \(SELECT lower\(email\) FROM "User" GROUP BY lower\(email\) HAVING count\(\*\) > 1\)`).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tc.dupes))
			if tc.dupes == 0 {
				mock.ExpectExec(`CREATE UNIQUE INDEX IF NOT EXISTS "User_email_lower_key" ON "User" \(lower\(email\)\)`).
					WillReturnResult(sqlmock.NewResult(0, 0))
			}
			err = database.ensureEmailIndex(context.Background())
			if tc.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr) || !errors.Is(err, ErrDuplicateEmails)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	database, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "User"`).WillReturnError(&pgconn.PgError{Code: "23505"})
	mock.ExpectRollback()
	if _, err := database.CreateUser(context.Background(), "carol@example.com", "hash"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("err = %v, want ErrEmailTaken", err)
	}
}
