package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// timePtr is a helper to create a pointer to a time value offset by duration
func timePtr(d time.Duration) *time.Time {
	t := time.Now().Add(d)
	return &t
}

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
			"id", "email", "passwordHash", "subscriptionStatus", "createdAt", "updatedAt",
			"disabled", "failedLoginAttempts", "lockedUntil", "lastFailedLogin",
		}).AddRow(
			"user123", "test@example.com", "hash", "free", time.Now(), time.Now(),
			false, 0, nil, nil,
		))
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "user123").
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

	// Set max attempts to 5
	os.Setenv("LOCKOUT_MAX_ATTEMPTS", "5")
	defer os.Unsetenv("LOCKOUT_MAX_ATTEMPTS")

	// Test recording failed login that triggers lockout (5th attempt)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user123", 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "email", "passwordHash", "subscriptionStatus", "createdAt", "updatedAt",
			"disabled", "failedLoginAttempts", "lockedUntil", "lastFailedLogin",
		}).AddRow(
			"user123", "test@example.com", "hash", "free", time.Now(), time.Now(),
			false, 4, nil, nil,
		))
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "user123").
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
		WithArgs(0, nil, nil, sqlmock.AnyArg(), "user123").
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
		name          string
		disabled      bool
		lockedUntil   *time.Time
		expectLocked  bool
		expectUntil   *time.Time
	}{
		{
			name:         "account disabled",
			disabled:     true,
			lockedUntil:  nil,
			expectLocked: true,
			expectUntil:  nil,
		},
		{
			name:         "account locked (future time)",
			disabled:     false,
			lockedUntil:  timePtr(10 * time.Minute),
			expectLocked: true,
		},
		{
			name:         "account not locked (past time)",
			disabled:     false,
			lockedUntil:  timePtr(-10 * time.Minute),
			expectLocked: false,
			expectUntil:  nil,
		},
		{
			name:         "account not locked or disabled",
			disabled:     false,
			lockedUntil:  nil,
			expectLocked: false,
			expectUntil:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := sqlmock.NewRows([]string{"disabled", "lockedUntil"})
			if tt.lockedUntil != nil {
				rows.AddRow(tt.disabled, *tt.lockedUntil)
			} else {
				rows.AddRow(tt.disabled, nil)
			}

			mock.ExpectQuery(`SELECT disabled, "lockedUntil" FROM "User"`).
				WithArgs("user123", 1).
				WillReturnRows(rows)

			ctx := context.Background()
			locked, until, err := db.IsAccountLocked(ctx, "user123")
			if err != nil {
				t.Fatalf("IsAccountLocked: %v", err)
			}

			if locked != tt.expectLocked {
				t.Errorf("expected locked=%v, got %v", tt.expectLocked, locked)
			}

			if tt.expectLocked && tt.lockedUntil != nil {
				if until == nil {
					t.Error("expected non-nil until time")
				}
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
