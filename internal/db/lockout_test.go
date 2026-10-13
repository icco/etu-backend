package db

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRecordFailedLogin(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	// Test recording failed login - first attempt
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user123", 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", colEmail, colPasswordHash, colSubscriptionStatus, colCreatedAt, "updatedAt",
			colDisabled, colFailedLoginAttempts, "lastFailedLogin",
		}).AddRow(
			"user123", "test@example.com", "hash", "free", time.Now(), time.Now(),
			false, 0, nil,
		))
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), "user123").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	err = db.RecordFailedLogin(ctx, "user123")
	if err != nil {
		t.Fatalf("RecordFailedLogin: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestRecordFailedLogin_LockAfterMaxAttempts(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	// Test recording failed login that triggers lockout (10th attempt)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user123", 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", colEmail, colPasswordHash, colSubscriptionStatus, colCreatedAt, "updatedAt",
			colDisabled, colFailedLoginAttempts, "lastFailedLogin",
		}).AddRow(
			"user123", "test@example.com", "hash", "free", time.Now(), time.Now(),
			false, 9, nil,
		))
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), "user123").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	err = db.RecordFailedLogin(ctx, "user123")
	if err != nil {
		t.Fatalf("RecordFailedLogin: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestRecordSuccessfulLogin(t *testing.T) {
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
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(0, nil, sqlmock.AnyArg(), "user123").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	err = db.RecordSuccessfulLogin(ctx, "user123")
	if err != nil {
		t.Fatalf("RecordSuccessfulLogin: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestIsAccountLocked(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	db, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	tests := []struct {
		name                string
		disabled            bool
		failedLoginAttempts int
		lastFailedAgo       time.Duration
		expectLocked        bool
	}{
		{
			name:                "account disabled",
			disabled:            true,
			failedLoginAttempts: 0,
			expectLocked:        true,
		},
		{
			name:                "account locked (5 recent failed attempts)",
			disabled:            false,
			failedLoginAttempts: 5,
			lastFailedAgo:       30 * time.Second,
			expectLocked:        true,
		},
		{
			name:                "lock expired after backoff window",
			disabled:            false,
			failedLoginAttempts: 5,
			lastFailedAgo:       2 * time.Minute,
			expectLocked:        false,
		},
		{
			name:                "account not locked (4 failed attempts)",
			disabled:            false,
			failedLoginAttempts: 4,
			lastFailedAgo:       time.Second,
			expectLocked:        false,
		},
		{
			name:                "account not locked or disabled",
			disabled:            false,
			failedLoginAttempts: 0,
			expectLocked:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := sqlmock.NewRows([]string{colDisabled, colFailedLoginAttempts, "lastFailedLogin"})
			rows.AddRow(tt.disabled, tt.failedLoginAttempts, time.Now().Add(-tt.lastFailedAgo))

			mock.ExpectQuery(`SELECT disabled, "failedLoginAttempts", "lastFailedLogin" FROM "User"`).
				WithArgs("user123", 1).
				WillReturnRows(rows)

			ctx := context.Background()
			locked, err := db.IsAccountLocked(ctx, "user123")
			if err != nil {
				t.Fatalf("IsAccountLocked: %v", err)
			}

			if locked != tt.expectLocked {
				t.Errorf("expected locked=%v, got %v", tt.expectLocked, locked)
			}
		})
	}
}

func TestDisableUser(t *testing.T) {
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
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(true, "Terms violation", sqlmock.AnyArg(), "user123").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	err = db.DisableUser(ctx, "user123", "Terms violation")
	if err != nil {
		t.Fatalf("DisableUser: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestEnableUser(t *testing.T) {
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
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(false, nil, sqlmock.AnyArg(), "user123").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx := context.Background()
	err = db.EnableUser(ctx, "user123")
	if err != nil {
		t.Fatalf("EnableUser: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}
