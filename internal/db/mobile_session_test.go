package db

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMobileSessionCapBeforeHash(t *testing.T) {
	for _, count := range []int{MaxMobileSessions, MaxMobileSessions + 1} {
		conn, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		database, err := NewFromConn(conn)
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT id FROM "User" WHERE id = \$1 FOR UPDATE`).WithArgs("owner").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("owner"))
		mock.ExpectQuery(`SELECT count\(\*\) FROM "ApiKey"`).WithArgs("owner", "etu-mobile").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
		mock.ExpectRollback()
		key, err := database.CreateMobileSession(context.Background(), "owner", func() (string, string, error) {
			t.Fatal("key hashing must not run when cap is reached")
			return "", "", nil
		})
		if !errors.Is(err, ErrMobileSessionLimit) || key != nil {
			t.Fatalf("expected session limit, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}
}
