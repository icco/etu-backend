package db

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/crypto/bcrypt"
)

type bcryptOf string

func (b bcryptOf) Match(v driver.Value) bool {
	s, ok := v.(string)
	return ok && bcrypt.CompareHashAndPassword([]byte(s), []byte(b)) == nil
}

func TestChangePasswordWithSettings(t *testing.T) {
	const current, next = "0ld-passw0rd", "n3w-passw0rd"
	hash, err := bcrypt.GenerateFromPassword([]byte(current), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	name := "Ada Lovelace"
	for _, tc := range []struct {
		name        string
		current     string
		missing     bool
		disabled    bool
		attempts    int
		deleteErr   error
		settingsErr error
		want        error
		wantAnyErr  bool
	}{
		{name: "success updates hash revokes keys and settings", current: current},
		{name: "wrong password records failure and commits", current: "wrong", want: ErrWrongPassword},
		{name: "missing user", current: current, missing: true, want: ErrUserNotFound},
		{name: "disabled", current: current, disabled: true, want: ErrAccountDisabled},
		{name: "locked", current: current, attempts: 5, want: ErrAccountLocked},
		{name: "revocation failure rolls back", current: current, deleteErr: errors.New("boom"), wantAnyErr: true},
		{name: "settings failure rolls back", current: current, settingsErr: errors.New("boom"), wantAnyErr: true},
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
			mock.ExpectBegin()
			rows := sqlmock.NewRows([]string{"id", colEmail, colPasswordHash, colDisabled, colFailedLoginAttempts, colLastFailedLogin, colCreatedAt})
			if !tc.missing {
				rows.AddRow("u1", "ada@example.com", string(hash), tc.disabled, tc.attempts, time.Now(), time.Now())
			}
			mock.ExpectQuery(`SELECT \* FROM "User" WHERE id = \$1 ORDER BY "User"."id" LIMIT \$2 FOR UPDATE`).WithArgs("u1", 1).WillReturnRows(rows)
			ok := !tc.missing && !tc.disabled && tc.attempts == 0
			switch {
			case ok && tc.current != current:
				mock.ExpectExec(`UPDATE "User" SET "failedLoginAttempts"="failedLoginAttempts" \+ 1`).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			case ok:
				mock.ExpectExec(`UPDATE "User" SET`).WithArgs(0, nil, bcryptOf(next), sqlmock.AnyArg(), "u1").WillReturnResult(sqlmock.NewResult(0, 1))
				del := mock.ExpectExec(`DELETE FROM "ApiKey" WHERE "userId" = \$1`).WithArgs("u1")
				if tc.deleteErr != nil {
					del.WillReturnError(tc.deleteErr)
					mock.ExpectRollback()
					break
				}
				del.WillReturnResult(sqlmock.NewResult(0, 3))
				sel := mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs("u1", 1)
				if tc.settingsErr != nil {
					sel.WillReturnError(tc.settingsErr)
					mock.ExpectRollback()
					break
				}
				sel.WillReturnRows(sqlmock.NewRows([]string{"id", colEmail, colCreatedAt}).AddRow("u1", "ada@example.com", time.Now()))
				mock.ExpectExec(`UPDATE "User" SET`).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(`SELECT \* FROM "User"`).WillReturnRows(sqlmock.NewRows([]string{"id", colEmail, colName, colCreatedAt}).AddRow("u1", "ada@example.com", name, time.Now()))
				mock.ExpectCommit()
			default:
				mock.ExpectRollback()
			}
			user, err := database.ChangePasswordWithSettings(context.Background(), "u1", tc.current, next, nil, &name, nil, nil)
			switch {
			case tc.want != nil:
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
			case tc.wantAnyErr:
				if err == nil {
					t.Fatal("expected error")
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if user == nil || user.Name == nil || *user.Name != name {
					t.Fatalf("user = %+v", user)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChangePasswordWithSettingsAndAI(t *testing.T) {
	const current, next = "0ld-passw0rd", "n3w-passw0rd"
	hash, err := bcrypt.GenerateFromPassword([]byte(current), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "opt in", false: "opt out"}[enabled], func(t *testing.T) {
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
			mock.ExpectQuery(`SELECT \* FROM "User" WHERE id = \$1 ORDER BY "User"."id" LIMIT \$2 FOR UPDATE`).WithArgs("u1", 1).
				WillReturnRows(sqlmock.NewRows([]string{"id", colPasswordHash, colCreatedAt}).AddRow("u1", string(hash), time.Now()))
			mock.ExpectExec(`UPDATE "User" SET`).WithArgs(0, nil, bcryptOf(next), sqlmock.AnyArg(), "u1").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(`DELETE FROM "ApiKey" WHERE "userId" = \$1`).WithArgs("u1").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs("u1", 1).
				WillReturnRows(sqlmock.NewRows([]string{"id", colCreatedAt}).AddRow("u1", time.Now()))
			mock.ExpectExec(`UPDATE "User" SET "aiEnabled"=\$1,"updatedAt"=\$2 WHERE "id" = \$3`).WithArgs(enabled, sqlmock.AnyArg(), "u1").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(`SELECT \* FROM "User"`).
				WillReturnRows(sqlmock.NewRows([]string{"id", colCreatedAt, "aiEnabled"}).AddRow("u1", time.Now(), enabled))
			mock.ExpectCommit()

			user, err := database.ChangePasswordWithSettingsAndAI(context.Background(), "u1", current, next, nil, nil, nil, nil, &enabled)
			if err != nil {
				t.Fatal(err)
			}
			if user.AIEnabled != enabled {
				t.Errorf("AIEnabled = %v, want %v", user.AIEnabled, enabled)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
