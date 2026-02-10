package service

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAuthenticate_AccountLockout(t *testing.T) {
	// Create mock database
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	database, err := db.NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	service := NewAuthService(database)

	// Create a password hash for "testpass"
	passwordHash, _ := bcrypt.GenerateFromPassword([]byte("testpass"), bcrypt.DefaultCost)

	t.Run("successful authentication clears failed attempts", func(t *testing.T) {
		ctx := context.Background()

		// Expect GetUserByEmail
		mock.ExpectQuery(`SELECT \* FROM "User"`).
			WithArgs("test@example.com", 1).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "email", "passwordHash", "subscriptionStatus", "createdAt", "updatedAt",
				"disabled", "failedLoginAttempts",
			}).AddRow(
				"user123", "test@example.com", string(passwordHash), "free", time.Now(), time.Now(),
				false, 3,
			))

		// Expect RecordSuccessfulLogin (clear attempts)
		mock.ExpectBegin()
		mock.ExpectExec(`UPDATE "User"`).
			WithArgs(0, nil, sqlmock.AnyArg(), "user123").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		resp, err := service.Authenticate(ctx, &pb.AuthenticateRequest{
			Email:    "test@example.com",
			Password: "testpass",
		})

		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if !resp.Success {
			t.Error("Expected successful authentication")
		}
		if resp.User == nil {
			t.Error("Expected user in response")
		}
	})

	t.Run("failed authentication records failed attempt", func(t *testing.T) {
		ctx := context.Background()

		// Expect GetUserByEmail
		mock.ExpectQuery(`SELECT \* FROM "User"`).
			WithArgs("test@example.com", 1).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "email", "passwordHash", "subscriptionStatus", "createdAt", "updatedAt",
				"disabled", "failedLoginAttempts",
			}).AddRow(
				"user123", "test@example.com", string(passwordHash), "free", time.Now(), time.Now(),
				false, 2,
			))

		// Expect RecordFailedLogin
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT \* FROM "User"`).
			WithArgs("user123", 1).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "email", "passwordHash", "subscriptionStatus", "createdAt", "updatedAt",
				"disabled", "failedLoginAttempts",
			}).AddRow(
				"user123", "test@example.com", string(passwordHash), "free", time.Now(), time.Now(),
				false, 2,
			))
		mock.ExpectExec(`UPDATE "User"`).
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "user123").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		resp, err := service.Authenticate(ctx, &pb.AuthenticateRequest{
			Email:    "test@example.com",
			Password: "wrongpass",
		})

		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if resp.Success {
			t.Error("Expected failed authentication")
		}
	})

	t.Run("disabled account returns error", func(t *testing.T) {
		ctx := context.Background()

		// Expect GetUserByEmail
		mock.ExpectQuery(`SELECT \* FROM "User"`).
			WithArgs("test@example.com", 1).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "email", "passwordHash", "subscriptionStatus", "createdAt", "updatedAt",
				"disabled", "disabledReason", "failedLoginAttempts",
			}).AddRow(
				"user123", "test@example.com", string(passwordHash), "free", time.Now(), time.Now(),
				true, "Terms violation", 0,
			))

		_, err := service.Authenticate(ctx, &pb.AuthenticateRequest{
			Email:    "test@example.com",
			Password: "testpass",
		})

		if err == nil {
			t.Fatal("Expected error for disabled account")
		}

		st, ok := status.FromError(err)
		if !ok {
			t.Fatal("Expected gRPC status error")
		}
		if st.Code() != codes.PermissionDenied {
			t.Errorf("Expected PermissionDenied code, got %v", st.Code())
		}
	})

	t.Run("locked account returns error", func(t *testing.T) {
		ctx := context.Background()

		// Expect GetUserByEmail
		mock.ExpectQuery(`SELECT \* FROM "User"`).
			WithArgs("test@example.com", 1).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "email", "passwordHash", "subscriptionStatus", "createdAt", "updatedAt",
				"disabled", "failedLoginAttempts",
			}).AddRow(
				"user123", "test@example.com", string(passwordHash), "free", time.Now(), time.Now(),
				false, 10,
			))

		_, err := service.Authenticate(ctx, &pb.AuthenticateRequest{
			Email:    "test@example.com",
			Password: "testpass",
		})

		if err == nil {
			t.Fatal("Expected error for locked account")
		}

		st, ok := status.FromError(err)
		if !ok {
			t.Fatal("Expected gRPC status error")
		}
		if st.Code() != codes.PermissionDenied {
			t.Errorf("Expected PermissionDenied code, got %v", st.Code())
		}
	})

	// Verify all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}
