package db

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const evictQuery = `DELETE FROM "ApiKey" WHERE id IN \(SELECT id FROM "ApiKey" WHERE "userId" = \$1 AND name = \$2 ORDER BY "lastUsed" ASC NULLS FIRST, "createdAt" ASC LIMIT \$3\)`

func TestCreateMobileSessionEviction(t *testing.T) {
	for _, tc := range []struct {
		name      string
		count     int
		evict     int
		deleted   int64
		deleteErr error
		wantErr   error
	}{
		{name: "under cap", count: MaxMobileSessions - 1},
		{name: "at cap evicts least recently used", count: MaxMobileSessions, evict: 1, deleted: 1},
		{name: "over cap evicts down to cap", count: MaxMobileSessions + 2, evict: 3, deleted: 3},
		{name: "delete failure rolls back", count: MaxMobileSessions, evict: 1, deleteErr: errors.New("database unavailable"), wantErr: errors.New("")},
		{name: "eviction cannot make room", count: MaxMobileSessions, evict: 1, deleted: 0, wantErr: ErrMobileSessionLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			database, err := NewFromConn(conn)
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT id FROM "User" WHERE id = \$1 FOR UPDATE`).WithArgs("owner").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("owner"))
			mock.ExpectQuery(`SELECT count\(\*\) FROM "ApiKey"`).WithArgs("owner", "etu-mobile").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tc.count))
			if tc.evict > 0 {
				del := mock.ExpectExec(evictQuery).WithArgs("owner", "etu-mobile", tc.evict)
				if tc.deleteErr != nil {
					del.WillReturnError(tc.deleteErr)
				} else {
					del.WillReturnResult(sqlmock.NewResult(0, tc.deleted))
				}
			}
			if tc.wantErr != nil {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec(`INSERT INTO "ApiKey"`).WithArgs(sqlmock.AnyArg(), "etu-mobile", "etu_prefix", "hash", "owner", sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			generated := false
			key, err := database.CreateMobileSession(context.Background(), "owner", func() (string, string, error) {
				generated = true
				return "etu_prefix", "hash", nil
			})
			if tc.wantErr != nil {
				if err == nil || key != nil {
					t.Fatalf("expected error and no key, got key=%v err=%v", key, err)
				}
				if errors.Is(tc.wantErr, ErrMobileSessionLimit) && !errors.Is(err, ErrMobileSessionLimit) {
					t.Fatalf("expected session limit, got %v", err)
				}
				if generated {
					t.Fatal("key hashing must not run when eviction fails")
				}
			} else if err != nil || key == nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
