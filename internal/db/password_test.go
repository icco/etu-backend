package db

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/crypto/bcrypt"
)

type bcryptOf string

func (b bcryptOf) Match(v driver.Value) bool {
	s, ok := v.(string)
	return ok && bcrypt.CompareHashAndPassword([]byte(s), []byte(b)) == nil
}

func TestChangePassword(t *testing.T) {
	for _, tc := range []struct {
		name      string
		updated   int64
		deleteErr error
		wantErr   bool
		wantFound bool
	}{
		{name: "updates hash and revokes keys", updated: 1, wantFound: true},
		{name: "missing user", updated: 0},
		{name: "revocation failure rolls back", updated: 1, deleteErr: errors.New("boom"), wantErr: true},
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
			mock.ExpectExec(`UPDATE "User" SET`).
				WithArgs(0, nil, bcryptOf("n3w-passw0rd"), sqlmock.AnyArg(), "u1").
				WillReturnResult(sqlmock.NewResult(0, tc.updated))
			if tc.updated == 0 {
				mock.ExpectRollback()
			} else {
				del := mock.ExpectExec(`DELETE FROM "ApiKey" WHERE "userId" = \$1`).WithArgs("u1")
				if tc.deleteErr != nil {
					del.WillReturnError(tc.deleteErr)
					mock.ExpectRollback()
				} else {
					del.WillReturnResult(sqlmock.NewResult(0, 3))
					mock.ExpectCommit()
				}
			}
			found, err := database.ChangePassword(context.Background(), "u1", "n3w-passw0rd")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.wantFound, found); diff != "" {
				t.Errorf("found (-want +got):\n%s", diff)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
