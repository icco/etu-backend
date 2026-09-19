package service

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/icco/etu-backend/internal/auth"
	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type captureKeyHash struct{ hash *string }

func (m captureKeyHash) Match(value driver.Value) bool {
	s, ok := value.(string)
	*m.hash = s
	return ok && strings.HasPrefix(s, "$2")
}

func TestLogin(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		email           string
		found, disabled bool
		attempts        int
		insertError     bool
		want            codes.Code
	}{
		{name: "success uses verified owner", email: testEmail, found: true, want: codes.OK},
		{name: "missing email", want: codes.InvalidArgument},
		{name: "unknown user", email: testEmail, want: codes.Unauthenticated},
		{name: "disabled", email: testEmail, found: true, disabled: true, want: codes.PermissionDenied},
		{name: "locked", email: testEmail, found: true, attempts: 10, want: codes.PermissionDenied},
		{name: "key storage failure", email: testEmail, found: true, insertError: true, want: codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			database, err := db.NewFromConn(conn)
			if err != nil {
				t.Fatal(err)
			}
			var storedHash string
			if tc.email != "" {
				rows := sqlmock.NewRows([]string{"id", "email", "passwordHash", "disabled", "failedLoginAttempts", "createdAt", "updatedAt"})
				if tc.found {
					rows.AddRow("verified-owner", tc.email, string(passwordHash), tc.disabled, tc.attempts, time.Now(), time.Now())
				}
				mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs(tc.email, 1).WillReturnRows(rows)
			}
			if tc.found && !tc.disabled && tc.attempts < 10 {
				mock.ExpectBegin()
				mock.ExpectExec(`UPDATE "User"`).WithArgs(0, nil, sqlmock.AnyArg(), "verified-owner").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				mock.ExpectBegin()
				insert := mock.ExpectExec(`INSERT INTO "ApiKey"`).WithArgs(sqlmock.AnyArg(), "etu-mobile", sqlmock.AnyArg(), captureKeyHash{&storedHash}, "verified-owner", sqlmock.AnyArg(), sqlmock.AnyArg())
				if tc.insertError {
					insert.WillReturnError(errors.New("database unavailable"))
					mock.ExpectRollback()
				} else {
					insert.WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit()
				}
			}
			ctx := auth.SetAuthContext(context.Background(), "attacker", "m2m")
			result, err := NewAuthService(database).Login(ctx, &pb.AuthenticateRequest{Email: tc.email, Password: testPassword})
			if status.Code(err) != tc.want {
				t.Fatalf("code = %v, want %v", status.Code(err), tc.want)
			}
			if tc.want == codes.OK {
				if len(result.RawKey) != 68 || !strings.HasPrefix(result.RawKey, "etu_") {
					t.Fatal("invalid key format")
				}
				if result.ApiKey.KeyPrefix != result.RawKey[:12] {
					t.Fatal("key prefix mismatch")
				}
				if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(result.RawKey)); err != nil {
					t.Fatal("stored hash does not match issued key")
				}
			} else if result != nil {
				t.Fatal("failed login returned session")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
