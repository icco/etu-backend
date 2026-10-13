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

func TestUpdateUserSettingsWithPassword(t *testing.T) {
	name := "Ada Lovelace"
	for _, tc := range []struct {
		name         string
		password     bool
		updated      int64
		deleteErr    error
		settingsErr  error
		wantErr      bool
		wantRollback bool
	}{
		{name: "settings only leaves keys", updated: 1},
		{name: "password updates hash revokes keys and settings", password: true, updated: 1},
		{name: "missing user", password: true, updated: 0, wantErr: true, wantRollback: true},
		{name: "revocation failure rolls back", password: true, updated: 1, deleteErr: errors.New("boom"), wantErr: true, wantRollback: true},
		{name: "settings failure rolls back password", password: true, updated: 1, settingsErr: errors.New("boom"), wantErr: true, wantRollback: true},
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
			if tc.password {
				mock.ExpectBegin()
			}
			var password *string
			if tc.password {
				p := "n3w-passw0rd"
				password = &p
				mock.ExpectExec(`UPDATE "User" SET`).
					WithArgs(0, nil, bcryptOf(p), sqlmock.AnyArg(), "u1").
					WillReturnResult(sqlmock.NewResult(0, tc.updated))
			}
			if tc.updated == 1 && tc.password {
				del := mock.ExpectExec(`DELETE FROM "ApiKey" WHERE "userId" = \$1`).WithArgs("u1")
				if tc.deleteErr != nil {
					del.WillReturnError(tc.deleteErr)
				} else {
					del.WillReturnResult(sqlmock.NewResult(0, 3))
				}
			}
			if tc.deleteErr == nil && (tc.updated == 1 || !tc.password) {
				sel := mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs("u1", 1)
				if tc.settingsErr != nil {
					sel.WillReturnError(tc.settingsErr)
				} else {
					sel.WillReturnRows(sqlmock.NewRows([]string{"id", colEmail, colCreatedAt}).AddRow("u1", "ada@example.com", time.Now()))
					if !tc.password {
						mock.ExpectBegin()
					}
					mock.ExpectExec(`UPDATE "User" SET`).WillReturnResult(sqlmock.NewResult(0, 1))
					if !tc.password {
						mock.ExpectCommit()
					}
					mock.ExpectQuery(`SELECT \* FROM "User"`).WillReturnRows(sqlmock.NewRows([]string{"id", colEmail, colName, colCreatedAt}).AddRow("u1", "ada@example.com", name, time.Now()))
				}
			}
			if tc.wantRollback {
				mock.ExpectRollback()
			} else if tc.password {
				mock.ExpectCommit()
			}
			user, err := database.UpdateUserSettingsWithPassword(context.Background(), "u1", nil, &name, password, nil, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && (user == nil || user.Name == nil || *user.Name != name) {
				t.Fatalf("user = %+v", user)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
