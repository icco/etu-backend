package service

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/db"
	pb "go.icco.me/etu-backend/proto"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type captureKeyHash struct{ hash *string }

func (m captureKeyHash) Match(value driver.Value) bool {
	s, ok := value.(string)
	*m.hash = s
	return ok && strings.HasPrefix(s, "$2")
}

// TestLoginLimitsForwardedIPAcrossEmails checks source-IP throttling independently of account limits.
func TestLoginLimitsForwardedIPAcrossEmails(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	database, err := db.NewFromConn(conn)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewAuthService(database)
	svc.loginLimits.global = rate.NewLimiter(rate.Inf, 30)
	ctx := auth.SetAuthContext(context.Background(), "service", "m2m")
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("x-etu-client-ip", "192.0.2.1"))
	for i := range 20 {
		email := fmt.Sprintf("alice%d@example.com", i)
		mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs(email, 1).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		if _, err := svc.Login(ctx, &pb.AuthenticateRequest{Email: email, Password: testPassword}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := svc.Login(ctx, &pb.AuthenticateRequest{Email: "bob@example.com", Password: testPassword}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("exhausted source IP was not throttled: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
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
		deleteError     bool
		sessions        int
		want            codes.Code
	}{
		{name: "success uses verified owner", email: testEmail, found: true, want: codes.OK},
		{name: "missing email", want: codes.InvalidArgument},
		{name: "unknown user", email: testEmail, want: codes.Unauthenticated},
		{name: "disabled", email: testEmail, found: true, disabled: true, want: codes.Unauthenticated},
		{name: "locked", email: testEmail, found: true, attempts: 5, want: codes.PermissionDenied},
		{name: "key storage failure", email: testEmail, found: true, insertError: true, want: codes.Internal},
		{name: "session cap evicts least recently used", email: testEmail, found: true, sessions: db.MaxMobileSessions, want: codes.OK},
		{name: "eviction failure", email: testEmail, found: true, sessions: db.MaxMobileSessions, deleteError: true, want: codes.Internal},
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
				rows := sqlmock.NewRows([]string{"id", "email", "passwordHash", "disabled", "failedLoginAttempts", "lastFailedLogin", "createdAt", "updatedAt"})
				if tc.found {
					rows.AddRow("verified-owner", tc.email, string(passwordHash), tc.disabled, tc.attempts, time.Now(), time.Now(), time.Now())
				}
				mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs(tc.email, 1).WillReturnRows(rows)
			}
			if tc.found && !tc.disabled && tc.attempts < 5 {
				mock.ExpectBegin()
				mock.ExpectExec(`UPDATE "User"`).WithArgs(0, nil, sqlmock.AnyArg(), "verified-owner").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				mock.ExpectBegin()
				mock.ExpectQuery(`SELECT id FROM "User" WHERE id = \$1 FOR UPDATE`).WithArgs("verified-owner").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("verified-owner"))
				mock.ExpectQuery(`SELECT count\(\*\) FROM "ApiKey"`).WithArgs("verified-owner", "etu-mobile").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tc.sessions))
				if tc.sessions >= db.MaxMobileSessions {
					del := mock.ExpectExec(`DELETE FROM "ApiKey" WHERE id IN \(SELECT id FROM "ApiKey" WHERE "userId" = \$1 AND name = \$2 ORDER BY "lastUsed" ASC NULLS FIRST, "createdAt" ASC LIMIT \$3\)`).WithArgs("verified-owner", "etu-mobile", tc.sessions-db.MaxMobileSessions+1)
					if tc.deleteError {
						del.WillReturnError(errors.New("database unavailable"))
					} else {
						del.WillReturnResult(sqlmock.NewResult(0, int64(tc.sessions-db.MaxMobileSessions+1)))
					}
				}
				if tc.deleteError {
					mock.ExpectRollback()
				} else {
					insert := mock.ExpectExec(`INSERT INTO "ApiKey"`).WithArgs(sqlmock.AnyArg(), "etu-mobile", sqlmock.AnyArg(), captureKeyHash{&storedHash}, "verified-owner", sqlmock.AnyArg(), sqlmock.AnyArg())
					if tc.insertError {
						insert.WillReturnError(errors.New("database unavailable"))
						mock.ExpectRollback()
					} else {
						insert.WillReturnResult(sqlmock.NewResult(0, 1))
						mock.ExpectCommit()
					}
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
