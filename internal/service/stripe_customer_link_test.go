package service

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"go.icco.me/etu-backend/internal/db"
	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestUpdateUserSubscriptionExpectedStripeCustomer(t *testing.T) {
	now := time.Now().UTC()
	columns := []string{"id", "email", "subscriptionStatus", "createdAt", "updatedAt", "stripeCustomerId"}
	tests := []struct {
		name     string
		expected *string
		rows     int64
		stored   string
		want     codes.Code
	}{
		{name: "match", expected: proto.String("cus_alice"), rows: 1, stored: "cus_alice", want: codes.OK},
		{name: "mismatch", expected: proto.String("cus_alice"), rows: 0, stored: "", want: codes.FailedPrecondition},
		{name: "absent", rows: 1, stored: "", want: codes.OK},
		{name: "empty", expected: proto.String(""), rows: 1, stored: "", want: codes.OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock.New: %v", err)
			}
			defer func() { _ = conn.Close() }()
			database, err := db.NewFromConn(conn)
			if err != nil {
				t.Fatalf("db.NewFromConn: %v", err)
			}
			expectStripeLock(mock, tt.rows > 0, nil)
			mock.ExpectExec(`UPDATE "User"`).WillReturnResult(sqlmock.NewResult(0, tt.rows))
			mock.ExpectQuery(`SELECT (.+) FROM "User"`).WillReturnRows(sqlmock.NewRows(columns).AddRow("alice", "alice@example.com", "active", now, now, tt.stored))
			if tt.rows > 0 {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			_, err = (&AuthService{db: database}).UpdateUserSubscription(stripeContext(t), &pb.UpdateUserSubscriptionRequest{
				UserId:                   "alice",
				SubscriptionStatus:       "active",
				ExpectedStripeCustomerId: tt.expected,
			})
			if diff := cmp.Diff(tt.want, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s (err=%v)", diff, err)
			}
			if tt.want == codes.FailedPrecondition {
				if diff := cmp.Diff("stripe customer link changed", status.Convert(err).Message()); diff != "" {
					t.Errorf("message mismatch (-want +got):\n%s", diff)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestUpdateUserSubscriptionPlayOwned(t *testing.T) {
	now := time.Now().UTC()
	columns := []string{"id", "email", "subscriptionStatus", "createdAt", "updatedAt", "billingProvider"}
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = conn.Close() }()
	database, err := db.NewFromConn(conn)
	if err != nil {
		t.Fatalf("db.NewFromConn: %v", err)
	}
	expectStripeLock(mock, false, nil)
	mock.ExpectExec(`UPDATE "User"`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT (.+) FROM "User"`).WillReturnRows(sqlmock.NewRows(columns).AddRow("alice", "alice@example.com", "active", now, now, "play"))
	mock.ExpectRollback()
	_, err = (&AuthService{db: database}).UpdateUserSubscription(stripeContext(t), &pb.UpdateUserSubscriptionRequest{
		UserId:             "alice",
		SubscriptionStatus: "active",
	})
	if diff := cmp.Diff(codes.FailedPrecondition, status.Code(err)); diff != "" {
		t.Errorf("code mismatch (-want +got):\n%s (err=%v)", diff, err)
	}
	if diff := cmp.Diff("billing managed by google play", status.Convert(err).Message()); diff != "" {
		t.Errorf("message mismatch (-want +got):\n%s", diff)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}
