package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
)

func TestUpdateUserSubscription_ExpectedStripeCustomer_SQL(t *testing.T) {
	const (
		userID   = "user-alice"
		expected = "cus_alice"
		eventID  = "evt_alice"
	)
	now := time.Now().UTC()
	userRow := func(stripe any) *sqlmock.Rows {
		return sqlmock.NewRows(userRowColumns).AddRow(userID, "alice@example.com", nil, "hash", "active", nil, now, stripe, nil, nil, now)
	}
	conditional := `UPDATE "User" SET .* WHERE id = \$\d+ AND \(\(.*\) AND "stripeCustomerId" = \$\d+`
	insertEvent := `INSERT INTO "StripeEvent"`

	tests := []struct {
		name    string
		in      SubscriptionUpdate
		setup   func(sqlmock.Sqlmock)
		wantErr error
		wantNil bool
	}{
		{
			name: "match applies update",
			in:   SubscriptionUpdate{SubscriptionStatus: "active", ExpectedStripeCustomerID: expected},
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectBegin()
				m.ExpectExec(conditional).WithArgs("stripe", false, "active", sqlmock.AnyArg(), userID, "stripe", "active", "trial", "trialing", "pro", "premium", (7 * 24 * time.Hour).Seconds(), expected).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectCommit()
				m.ExpectQuery(`SELECT (.+) FROM "User"`).WillReturnRows(userRow(expected))
			},
		},
		{
			name: "mismatch applies nothing",
			in:   SubscriptionUpdate{SubscriptionStatus: "active", ExpectedStripeCustomerID: expected},
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectBegin()
				m.ExpectExec(conditional).WillReturnResult(sqlmock.NewResult(0, 0))
				m.ExpectCommit()
				m.ExpectQuery(`SELECT (.+) FROM "User"`).WillReturnRows(userRow("cus_bob"))
			},
			wantErr: ErrStripeCustomerMismatch,
			wantNil: true,
		},
		{
			name: "cleared link is a mismatch",
			in:   SubscriptionUpdate{SubscriptionStatus: "active", ExpectedStripeCustomerID: expected},
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectBegin()
				m.ExpectExec(conditional).WillReturnResult(sqlmock.NewResult(0, 0))
				m.ExpectCommit()
				m.ExpectQuery(`SELECT (.+) FROM "User"`).WillReturnRows(userRow(""))
			},
			wantErr: ErrStripeCustomerMismatch,
			wantNil: true,
		},
		{
			name: "missing user stays not found",
			in:   SubscriptionUpdate{SubscriptionStatus: "active", ExpectedStripeCustomerID: expected},
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectBegin()
				m.ExpectExec(conditional).WillReturnResult(sqlmock.NewResult(0, 0))
				m.ExpectCommit()
				m.ExpectQuery(`SELECT (.+) FROM "User"`).WillReturnRows(sqlmock.NewRows(userRowColumns))
			},
			wantNil: true,
		},
		{
			name: "mismatch with event rolls back event",
			in:   SubscriptionUpdate{SubscriptionStatus: "active", ExpectedStripeCustomerID: expected, StripeEventID: eventID},
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectBegin()
				m.ExpectExec(insertEvent).WithArgs(eventID, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectExec(conditional).WillReturnResult(sqlmock.NewResult(0, 0))
				m.ExpectQuery(`SELECT (.+) FROM "User"`).WillReturnRows(userRow(""))
				m.ExpectRollback()
			},
			wantErr: ErrStripeCustomerMismatch,
			wantNil: true,
		},
		{
			name: "match with event records event",
			in:   SubscriptionUpdate{SubscriptionStatus: "active", ExpectedStripeCustomerID: expected, StripeEventID: eventID},
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectBegin()
				m.ExpectExec(insertEvent).WithArgs(eventID, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectExec(conditional).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectQuery(`SELECT (.+) FROM "User"`).WillReturnRows(userRow(expected))
				m.ExpectCommit()
			},
		},
		{
			name: "duplicate event returns current user without update",
			in:   SubscriptionUpdate{SubscriptionStatus: "active", ExpectedStripeCustomerID: expected, StripeEventID: eventID},
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectBegin()
				m.ExpectExec(insertEvent).WithArgs(eventID, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))
				m.ExpectQuery(`SELECT (.+) FROM "User"`).WillReturnRows(userRow(""))
				m.ExpectCommit()
			},
		},
		{
			name: "absent expectation updates unconditionally",
			in:   SubscriptionUpdate{SubscriptionStatus: "active"},
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectBegin()
				m.ExpectExec(`UPDATE "User" SET .* WHERE id = \$\d+ AND \(\(.*\)\)$`).WithArgs("stripe", false, "active", sqlmock.AnyArg(), userID, "stripe", "active", "trial", "trialing", "pro", "premium", (7 * 24 * time.Hour).Seconds()).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectCommit()
				m.ExpectQuery(`SELECT (.+) FROM "User"`).WillReturnRows(userRow(""))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock.New: %v", err)
			}
			defer func() { _ = sqlDB.Close() }()
			db, err := NewFromConn(sqlDB)
			if err != nil {
				t.Fatalf("NewFromConn: %v", err)
			}
			tt.setup(mock)
			user, err := db.UpdateUserSubscription(context.Background(), userID, tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.wantNil, user == nil); diff != "" {
				t.Errorf("nil user mismatch (-want +got):\n%s", diff)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}
