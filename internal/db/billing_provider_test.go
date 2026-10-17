package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
)

const (
	playUserID  = "user-ada"
	playToken   = "token-new"
	playOldTok  = "token-old"
	upsertPlay  = `INSERT INTO "PlaySubscription" .* ON CONFLICT \("purchaseToken"\) DO UPDATE SET .* RETURNING "superseded"`
	supersedeQ  = `UPDATE "PlaySubscription" SET .* WHERE "purchaseToken" = \$\d+ AND "userId" = \$\d+`
	ownedWhere  = `WHERE id = \$\d+ AND \(\("billingProvider" IS NULL OR "billingProvider" = \$\d+ OR NOT COALESCE\(`
	userSelectQ = `SELECT (.+) FROM "User"`
)

func newPlayTestDB(t *testing.T) (*DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	d, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}
	return d, mock
}

func playUserRow() *sqlmock.Rows {
	now := time.Now().UTC()
	return sqlmock.NewRows(userRowColumns).AddRow(playUserID, "ada@example.com", nil, "hash", "active", nil, now, nil, nil, nil, now)
}

func supersededRow(v bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"superseded"}).AddRow(v)
}

func TestOwnershipClause(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		grace    time.Duration
		wantArgs []any
	}{
		{
			name:     "play with one hour grace",
			provider: BillingProviderPlay,
			grace:    time.Hour,
			wantArgs: []any{"play", []string{"active", "trial", "trialing", "pro", "premium"}, float64(3600)},
		},
		{
			name:     "stripe with zero grace",
			provider: BillingProviderStripe,
			wantArgs: []any{"stripe", []string{"active", "trial", "trialing", "pro", "premium"}, float64(0)},
		},
	}
	const wantSQL = `"billingProvider" IS NULL OR "billingProvider" = ? OR NOT COALESCE(("subscriptionStatus" IN ? AND "subscriptionStatus" <> 'past_due') OR ("subscriptionStatus" = 'past_due' AND "pastDueSince" > now() - make_interval(secs => ?)), false)`
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSQL, gotArgs := ownershipClause(tt.provider, tt.grace)
			if diff := cmp.Diff(wantSQL, gotSQL); diff != "" {
				t.Errorf("sql mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantArgs, gotArgs); diff != "" {
				t.Errorf("args mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestApplyPlaySubscriptionClaimsUnownedUser(t *testing.T) {
	expiry := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	in := PlaySubscriptionUpdate{
		PurchaseToken: playToken, UserID: playUserID, ProductID: "etu_pro", BasePlanID: "monthly",
		State: "SUBSCRIPTION_STATE_ACTIVE", ExpiryTime: &expiry, AutoRenewing: true, Status: "active",
	}
	d, mock := newPlayTestDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(upsertPlay).WillReturnRows(supersededRow(false))
	mock.ExpectExec(`UPDATE "User" SET "billingProvider"=\$1,.* `+ownedWhere).
		WithArgs("play", false, expiry, "active", sqlmock.AnyArg(), playUserID, "play",
			"active", "trial", "trialing", "pro", "premium", float64(3600)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(userSelectQ).WillReturnRows(playUserRow())

	user, err := d.ApplyPlaySubscription(context.Background(), in, time.Hour)
	if err != nil {
		t.Fatalf("ApplyPlaySubscription: %v", err)
	}
	if user == nil || user.ID != playUserID {
		t.Fatalf("user = %+v, want id %q", user, playUserID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestApplyPlaySubscriptionRejectedWhenStripeOwnsPaidUser(t *testing.T) {
	in := PlaySubscriptionUpdate{PurchaseToken: playToken, UserID: playUserID, State: "SUBSCRIPTION_STATE_ACTIVE", Status: "active"}
	d, mock := newPlayTestDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(upsertPlay).WillReturnRows(supersededRow(false))
	mock.ExpectExec(`UPDATE "User" SET .* ` + ownedWhere).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	user, err := d.ApplyPlaySubscription(context.Background(), in, time.Hour)
	if !errors.Is(err, ErrBillingProviderMismatch) {
		t.Fatalf("err = %v, want %v", err, ErrBillingProviderMismatch)
	}
	if user != nil {
		t.Errorf("user = %+v, want nil", user)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestApplyPlaySubscriptionSupersedesLinkedToken(t *testing.T) {
	linked := playOldTok
	in := PlaySubscriptionUpdate{
		PurchaseToken: playToken, UserID: playUserID, State: "SUBSCRIPTION_STATE_ACTIVE",
		LinkedPurchaseToken: &linked, Status: "active",
	}
	d, mock := newPlayTestDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(upsertPlay).WillReturnRows(supersededRow(false))
	mock.ExpectExec(supersedeQ).WithArgs(true, sqlmock.AnyArg(), playOldTok, playUserID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "User" SET .* ` + ownedWhere).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(userSelectQ).WillReturnRows(playUserRow())

	if _, err := d.ApplyPlaySubscription(context.Background(), in, time.Hour); err != nil {
		t.Fatalf("ApplyPlaySubscription: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestApplyPlaySubscriptionSkipsUserWrite(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		superseded bool
	}{
		{name: "superseded row", status: "active", superseded: true},
		{name: "empty status", status: "", superseded: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := PlaySubscriptionUpdate{PurchaseToken: playToken, UserID: playUserID, State: "SUBSCRIPTION_STATE_EXPIRED", Status: tt.status}
			d, mock := newPlayTestDB(t)
			mock.ExpectBegin()
			mock.ExpectQuery(upsertPlay).WillReturnRows(supersededRow(tt.superseded))
			mock.ExpectCommit()
			mock.ExpectQuery(userSelectQ).WillReturnRows(playUserRow())

			user, err := d.ApplyPlaySubscription(context.Background(), in, time.Hour)
			if err != nil {
				t.Fatalf("ApplyPlaySubscription: %v", err)
			}
			if user == nil {
				t.Fatal("user = nil, want current user")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestUpdateUserSubscriptionRejectedWhenPlayOwns(t *testing.T) {
	d, mock := newPlayTestDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "User" SET "billingProvider"=\$1,.* `+ownedWhere).
		WithArgs("stripe", false, "active", sqlmock.AnyArg(), playUserID, "stripe",
			"active", "trial", "trialing", "pro", "premium", float64((7 * 24 * time.Hour).Seconds())).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	now := time.Now().UTC()
	mock.ExpectQuery(userSelectQ).WillReturnRows(
		sqlmock.NewRows(append(append([]string{}, userRowColumns...), colBillingProvider)).
			AddRow(playUserID, "ada@example.com", nil, "hash", "active", nil, now, nil, nil, nil, now, "play"))

	user, err := d.UpdateUserSubscription(context.Background(), playUserID, SubscriptionUpdate{SubscriptionStatus: "active"})
	if !errors.Is(err, ErrBillingProviderMismatch) {
		t.Fatalf("err = %v, want %v", err, ErrBillingProviderMismatch)
	}
	if user != nil {
		t.Errorf("user = %+v, want nil", user)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestUpdateUserSubscriptionTakesOverLapsedPlayUser(t *testing.T) {
	d, mock := newPlayTestDB(t)
	d.SetPaywallGrace(time.Hour)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "User" SET "billingProvider"=\$1,.* `+ownedWhere).
		WithArgs("stripe", false, "active", sqlmock.AnyArg(), playUserID, "stripe",
			"active", "trial", "trialing", "pro", "premium", float64(3600)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(userSelectQ).WillReturnRows(playUserRow())

	user, err := d.UpdateUserSubscription(context.Background(), playUserID, SubscriptionUpdate{SubscriptionStatus: "active"})
	if err != nil {
		t.Fatalf("UpdateUserSubscription: %v", err)
	}
	if user == nil {
		t.Fatal("user = nil, want updated user")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestPlaySubscriptionUser(t *testing.T) {
	tests := []struct {
		name string
		rows *sqlmock.Rows
		want string
	}{
		{name: "known token", rows: sqlmock.NewRows([]string{"userId"}).AddRow(playUserID), want: playUserID},
		{name: "unknown token", rows: sqlmock.NewRows([]string{"userId"}), want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, mock := newPlayTestDB(t)
			mock.ExpectQuery(`SELECT "userId" FROM "PlaySubscription" WHERE "purchaseToken" = \$1`).WithArgs(playToken, 1).WillReturnRows(tt.rows)
			got, err := d.PlaySubscriptionUser(context.Background(), playToken)
			if err != nil {
				t.Fatalf("PlaySubscriptionUser: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("userId mismatch (-want +got):\n%s", diff)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestActivePlaySubscriptions(t *testing.T) {
	d, mock := newPlayTestDB(t)
	now := time.Now().UTC()
	cols := []string{"purchaseToken", "userId", "productId", "basePlanId", "state", "createdAt", "updatedAt"}
	mock.ExpectQuery(`SELECT \* FROM "PlaySubscription" WHERE "userId" = \$1 AND NOT "superseded" AND "state" NOT IN \(\$2,\$3\)`).
		WithArgs(playUserID, "SUBSCRIPTION_STATE_EXPIRED", "SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED").
		WillReturnRows(sqlmock.NewRows(cols).AddRow(playToken, playUserID, "etu_pro", "monthly", "SUBSCRIPTION_STATE_ACTIVE", now, now))

	got, err := d.ActivePlaySubscriptions(context.Background(), playUserID)
	if err != nil {
		t.Fatalf("ActivePlaySubscriptions: %v", err)
	}
	if len(got) != 1 || got[0].PurchaseToken != playToken {
		t.Errorf("subscriptions = %+v, want one with token %q", got, playToken)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestApplyPlaySubscriptionRejectsTokenOfOtherUser(t *testing.T) {
	in := PlaySubscriptionUpdate{PurchaseToken: playToken, UserID: playUserID, State: "SUBSCRIPTION_STATE_ACTIVE", Status: "active"}
	d, mock := newPlayTestDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(upsertPlay + `,"userId"`).
		WillReturnRows(sqlmock.NewRows([]string{"superseded", "userId"}).AddRow(false, "user-bob"))
	mock.ExpectRollback()

	user, err := d.ApplyPlaySubscription(context.Background(), in, time.Hour)
	if !errors.Is(err, ErrPlayTokenUserMismatch) {
		t.Fatalf("err = %v, want %v", err, ErrPlayTokenUserMismatch)
	}
	if user != nil {
		t.Errorf("user = %+v, want nil", user)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestReleaseStripeOwnership(t *testing.T) {
	d, mock := newPlayTestDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "User" SET "cancelAtPeriodEnd"=\$1,"pastDueSince"=NULL,"subscriptionStatus"=\$2,"updatedAt"=\$3 WHERE id = \$4 AND "billingProvider" = \$5`).
		WithArgs(false, "canceled", sqlmock.AnyArg(), playUserID, "stripe").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := d.ReleaseStripeOwnership(context.Background(), playUserID); err != nil {
		t.Fatalf("ReleaseStripeOwnership: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestMarkPlayAcknowledged(t *testing.T) {
	d, mock := newPlayTestDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "PlaySubscription" SET "acknowledged"=\$1,"updatedAt"=\$2 WHERE "purchaseToken" = \$3`).
		WithArgs(true, sqlmock.AnyArg(), playToken).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := d.MarkPlayAcknowledged(context.Background(), playToken); err != nil {
		t.Fatalf("MarkPlayAcknowledged: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestLapsedPlayOwner(t *testing.T) {
	tests := []struct {
		name  string
		count int
		want  bool
	}{
		{name: "play owns unpaid user", count: 1, want: true},
		{name: "play owns paid user or not play", count: 0, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, mock := newPlayTestDB(t)
			d.SetPaywallGrace(time.Hour)
			mock.ExpectQuery(`SELECT count\(\*\) FROM "User" WHERE \(id = \$1 AND "billingProvider" = \$2\) AND \(\("billingProvider" IS NULL OR "billingProvider" = \$3 OR NOT COALESCE\(`).
				WithArgs(playUserID, "play", "stripe", "active", "trial", "trialing", "pro", "premium", float64(3600)).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tt.count))

			got, err := d.LapsedPlayOwner(context.Background(), playUserID)
			if err != nil {
				t.Fatalf("LapsedPlayOwner: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("lapsed mismatch (-want +got):\n%s", diff)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}
