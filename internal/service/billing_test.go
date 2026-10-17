package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/billing"
	"go.icco.me/etu-backend/internal/db"
	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	playTok              = "token-alice"
	playProduct          = "etu"
	stateActive          = "SUBSCRIPTION_STATE_ACTIVE"
	stateExpired         = "SUBSCRIPTION_STATE_EXPIRED"
	stateCancel          = "SUBSCRIPTION_STATE_CANCELED"
	statePending         = "SUBSCRIPTION_STATE_PENDING"
	statePendingCanceled = "SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED"
	provStripe           = "stripe"
	provPlay             = "play"
	upsertPlayQ          = `INSERT INTO "PlaySubscription" .* RETURNING "superseded","userId"`
	selectUserQ          = `SELECT \* FROM "User"`
	purchaseOwnerQ       = `SELECT "userId" FROM "PlaySubscription" WHERE "purchaseToken" = \$1`
	releaseQ             = `UPDATE "User" SET "cancelAtPeriodEnd"=\$1,"pastDueSince"=NULL,"subscriptionStatus"=\$2,"updatedAt"=\$3 WHERE id = \$4 AND "billingProvider" = \$5`
	markAckQ             = `UPDATE "PlaySubscription" SET "acknowledged"=\$1`
	lapsedPlayQ          = `SELECT count\(\*\) FROM "User" WHERE \(id = \$1 AND "billingProvider" = \$2\)`
	activePlayQ          = `SELECT \* FROM "PlaySubscription" WHERE "userId" = \$1 AND NOT "superseded"`
	playUpdateQ          = `UPDATE "User" SET "billingProvider"=\$1,"cancelAtPeriodEnd"=\$2,"pastDueSince"=NULL,"subscriptionEnd"=\$3,"subscriptionStatus"=\$4`
	stripeUpdate         = `UPDATE "User" SET "billingProvider"=\$1`
)

var (
	playExpiry         = time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	billingUserColumns = []string{"id", colEmail, colSubscriptionStatus, "billingProvider", "stripeCustomerId", "stripeSubscriptionId", colCreatedAt, colUpdatedAt}
)

type fakePlay struct {
	purchases map[string]*billing.PlayPurchase
	getErr    error
	cancelErr error
	acks      []string
	cancels   []string
}

func (f *fakePlay) GetSubscription(_ context.Context, token string) (*billing.PlayPurchase, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	p, ok := f.purchases[token]
	if !ok {
		return nil, billing.ErrPlayPurchaseNotFound
	}
	cp := *p
	return &cp, nil
}

func (f *fakePlay) Acknowledge(_ context.Context, productID, token string) error {
	f.acks = append(f.acks, productID+"/"+token)
	f.purchases[token].Acknowledged = true
	return nil
}

func (f *fakePlay) Cancel(_ context.Context, token string) error {
	f.cancels = append(f.cancels, token)
	return f.cancelErr
}

func alicePurchase(state string) *billing.PlayPurchase {
	exp := playExpiry
	return &billing.PlayPurchase{
		State: state, ProductID: playProduct, BasePlanID: "yearly", ExpiryTime: &exp,
		AutoRenewing: true, FreeTrial: state == stateActive, ObfuscatedAccountID: acctUser,
	}
}

func newBillingMock(t *testing.T) (*db.DB, sqlmock.Sqlmock) {
	t.Helper()
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	database, err := db.NewFromConn(conn)
	if err != nil {
		t.Fatalf("db.NewFromConn: %v", err)
	}
	return database, mock
}

func billingUserRow(subStatus string, provider any) *sqlmock.Rows {
	now := time.Now().UTC()
	var cus, sub any
	if provider == provStripe {
		cus, sub = "cus_alice", "sub_alice"
	}
	return sqlmock.NewRows(billingUserColumns).AddRow(acctUser, testEmail, subStatus, provider, cus, sub, now, now)
}

func expectTokenOwner(mock sqlmock.Sqlmock, owner string) {
	rows := sqlmock.NewRows([]string{"userId"})
	if owner != "" {
		rows.AddRow(owner)
	}
	mock.ExpectQuery(purchaseOwnerQ).WithArgs(playTok, 1).WillReturnRows(rows)
}

func expectBillingUser(mock sqlmock.Sqlmock, subStatus string, provider any) {
	mock.ExpectQuery(selectUserQ).WithArgs(acctUser, 1).WillReturnRows(billingUserRow(subStatus, provider))
}

// expectPlayApply mocks ApplyPlaySubscription writing status; rows is 0 when ownership blocks the write.
func expectPlayApply(mock sqlmock.Sqlmock, subStatus string, rows int64) {
	mock.ExpectBegin()
	mock.ExpectQuery(upsertPlayQ).WillReturnRows(sqlmock.NewRows([]string{"superseded", "userId"}).AddRow(false, acctUser))
	if subStatus != "" {
		mock.ExpectExec(playUpdateQ).
			WithArgs(provPlay, false, playExpiry, subStatus, sqlmock.AnyArg(), acctUser, provPlay,
				"active", "trial", "trialing", "pro", "premium", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, rows))
	}
	mock.ExpectCommit()
	if rows > 0 || subStatus == "" {
		provider := any(provPlay)
		if subStatus == "" {
			provider = nil
		}
		expectBillingUser(mock, subStatus, provider)
	}
}

func expectNotLapsedPlay(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(lapsedPlayQ).WithArgs(acctUser, provPlay, provStripe, "active", "trial", "trialing", "pro", "premium", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
}

func expectMarkAck(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec(markAckQ).WithArgs(true, sqlmock.AnyArg(), playTok).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func expectRelease(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec(releaseQ).WithArgs(false, "canceled", sqlmock.AnyArg(), acctUser, provStripe).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func aliceCtx() context.Context {
	return auth.SetAuthContext(context.Background(), acctUser, "apikey")
}

func verifyReq() *pb.VerifyPlayPurchaseRequest {
	return &pb.VerifyPlayPurchaseRequest{PurchaseToken: playTok, ProductId: playProduct}
}

func newTestBillingService(database *db.DB, play *fakePlay, stripe SubscriptionCanceler, allowTest bool) *BillingService {
	return NewBillingService(&PlaySync{DB: database, Play: play, Stripe: stripe, AllowTest: allowTest, Grace: time.Hour}, "com.timeclimbers.etu")
}

func TestVerifyPlayPurchaseActivatesTrial(t *testing.T) {
	database, mock := newBillingMock(t)
	play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: alicePurchase(stateActive)}}
	stripe := &fakeCanceler{}
	expectTokenOwner(mock, "")
	expectBillingUser(mock, "", nil)
	expectPlayApply(mock, "trialing", 1)
	expectMarkAck(mock)

	resp, err := newTestBillingService(database, play, stripe, false).VerifyPlayPurchase(aliceCtx(), verifyReq())
	if err != nil {
		t.Fatalf("VerifyPlayPurchase: %v", err)
	}
	if diff := cmp.Diff("trialing", resp.GetUser().GetSubscriptionStatus()); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(provPlay, resp.GetUser().GetBillingProvider()); diff != "" {
		t.Errorf("billing provider mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{playProduct + "/" + playTok}, play.acks); diff != "" {
		t.Errorf("acks mismatch (-want +got):\n%s", diff)
	}
	if len(stripe.calls) != 0 {
		t.Errorf("stripe calls = %v, want none", stripe.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestVerifyPlayPurchaseIdempotent(t *testing.T) {
	database, mock := newBillingMock(t)
	play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: alicePurchase(stateActive)}}
	expectTokenOwner(mock, "")
	expectBillingUser(mock, "", nil)
	expectPlayApply(mock, "trialing", 1)
	expectMarkAck(mock)
	expectTokenOwner(mock, acctUser)
	expectBillingUser(mock, "trialing", provPlay)
	expectPlayApply(mock, "trialing", 1)

	svc := newTestBillingService(database, play, &fakeCanceler{}, false)
	for i := range 2 {
		if _, err := svc.VerifyPlayPurchase(aliceCtx(), verifyReq()); err != nil {
			t.Fatalf("call %d: VerifyPlayPurchase: %v", i, err)
		}
	}
	if diff := cmp.Diff([]string{playProduct + "/" + playTok}, play.acks); diff != "" {
		t.Errorf("acks mismatch (-want +got):\n%s", diff)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestVerifyPlayPurchaseAccountMismatch(t *testing.T) {
	tests := []struct {
		name       string
		obfuscated string
		setup      func(sqlmock.Sqlmock)
	}{
		{name: "obfuscated account is another user", obfuscated: acctOther, setup: func(sqlmock.Sqlmock) {}},
		{name: "obfuscated account missing", obfuscated: "", setup: func(sqlmock.Sqlmock) {}},
		{name: "token stored for another user", obfuscated: acctUser, setup: func(m sqlmock.Sqlmock) { expectTokenOwner(m, acctOther) }},
		{
			name:       "token claimed by another user during write",
			obfuscated: acctUser,
			setup: func(m sqlmock.Sqlmock) {
				expectTokenOwner(m, "")
				expectBillingUser(m, "", nil)
				m.ExpectBegin()
				m.ExpectQuery(upsertPlayQ).WillReturnRows(sqlmock.NewRows([]string{"superseded", "userId"}).AddRow(false, acctOther))
				m.ExpectRollback()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newBillingMock(t)
			p := alicePurchase(stateActive)
			p.ObfuscatedAccountID = tt.obfuscated
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: p}}
			tt.setup(mock)

			_, err := newTestBillingService(database, play, &fakeCanceler{}, false).VerifyPlayPurchase(aliceCtx(), verifyReq())
			if diff := cmp.Diff(codes.PermissionDenied, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s (err=%v)", diff, err)
			}
			if len(play.acks) != 0 {
				t.Errorf("acks = %v, want none", play.acks)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestVerifyPlayPurchaseTakesOverLapsedStripe(t *testing.T) {
	tests := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
	}{
		{
			name: "lapsed stripe owner",
			setup: func(m sqlmock.Sqlmock) {
				expectTokenOwner(m, "")
				expectBillingUser(m, "past_due", provStripe)
				expectRelease(m)
				expectPlayApply(m, "trialing", 1)
			},
		},
		{
			name: "stripe takes ownership during the write",
			setup: func(m sqlmock.Sqlmock) {
				expectTokenOwner(m, "")
				expectBillingUser(m, "", nil)
				expectPlayApply(m, "trialing", 0)
				expectBillingUser(m, "active", provStripe)
				expectRelease(m)
				expectPlayApply(m, "trialing", 1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newBillingMock(t)
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: alicePurchase(stateActive)}}
			stripe := &fakeCanceler{}
			tt.setup(mock)
			expectMarkAck(mock)

			resp, err := newTestBillingService(database, play, stripe, false).VerifyPlayPurchase(aliceCtx(), verifyReq())
			if err != nil {
				t.Fatalf("VerifyPlayPurchase: %v", err)
			}
			if diff := cmp.Diff([]string{"cus_alice/sub_alice"}, stripe.calls); diff != "" {
				t.Errorf("stripe calls mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(provPlay, resp.GetUser().GetBillingProvider()); diff != "" {
				t.Errorf("billing provider mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]string{playProduct + "/" + playTok}, play.acks); diff != "" {
				t.Errorf("acks mismatch (-want +got):\n%s", diff)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestVerifyPlayPurchaseTakeoverCancelFails(t *testing.T) {
	tests := []struct {
		name   string
		stripe SubscriptionCanceler
	}{
		{name: "stripe cancel fails", stripe: &fakeCanceler{err: errors.New("stripe down")}},
		{name: "stripe not configured", stripe: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newBillingMock(t)
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: alicePurchase(stateActive)}}
			expectTokenOwner(mock, "")
			expectBillingUser(mock, "active", provStripe)

			_, err := newTestBillingService(database, play, tt.stripe, false).VerifyPlayPurchase(aliceCtx(), verifyReq())
			if diff := cmp.Diff(codes.Unavailable, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s (err=%v)", diff, err)
			}
			if msg := status.Convert(err).Message(); msg == db.ErrBillingProviderMismatch.Error() {
				t.Errorf("message = %q, must not claim google play billing", msg)
			}
			if len(play.acks) != 0 {
				t.Errorf("acks = %v, want none", play.acks)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestVerifyPlayPurchaseNoAccessChange(t *testing.T) {
	tests := []struct {
		name  string
		state string
		setup func(sqlmock.Sqlmock)
	}{
		{
			name:  "pending purchase writes no status",
			state: statePending,
			setup: func(m sqlmock.Sqlmock) {
				expectTokenOwner(m, "")
				expectBillingUser(m, "", nil)
				expectPlayApply(m, "", 0)
			},
		},
		{
			name:  "expired purchase leaves paid stripe owner",
			state: stateExpired,
			setup: func(m sqlmock.Sqlmock) {
				expectTokenOwner(m, "")
				expectBillingUser(m, "active", provStripe)
				expectPlayApply(m, "canceled", 0)
				expectBillingUser(m, "active", provStripe)
			},
		},
		{
			name:  "canceled purchase does not take over stripe",
			state: stateCancel,
			setup: func(m sqlmock.Sqlmock) {
				expectTokenOwner(m, "")
				expectBillingUser(m, "active", provStripe)
				m.ExpectBegin()
				m.ExpectQuery(upsertPlayQ).WillReturnRows(sqlmock.NewRows([]string{"superseded", "userId"}).AddRow(false, acctUser))
				m.ExpectExec(`UPDATE "User" SET`).WillReturnResult(sqlmock.NewResult(0, 0))
				m.ExpectCommit()
				expectBillingUser(m, "active", provStripe)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newBillingMock(t)
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: alicePurchase(tt.state)}}
			stripe := &fakeCanceler{}
			tt.setup(mock)

			if _, err := newTestBillingService(database, play, stripe, false).VerifyPlayPurchase(aliceCtx(), verifyReq()); err != nil {
				t.Fatalf("VerifyPlayPurchase: %v", err)
			}
			if len(play.acks) != 0 {
				t.Errorf("acks = %v, want none", play.acks)
			}
			if len(stripe.calls) != 0 {
				t.Errorf("stripe calls = %v, want none", stripe.calls)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestVerifyPlayPurchaseRejectsTestPurchase(t *testing.T) {
	tests := []struct {
		name      string
		allowTest bool
		want      codes.Code
	}{
		{name: "test purchases not allowed", want: codes.FailedPrecondition},
		{name: "test purchases allowed", allowTest: true, want: codes.OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newBillingMock(t)
			p := alicePurchase(stateActive)
			p.Test = true
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: p}}
			if tt.allowTest {
				expectTokenOwner(mock, "")
				expectBillingUser(mock, "", nil)
				expectPlayApply(mock, "trialing", 1)
				expectMarkAck(mock)
			}

			_, err := newTestBillingService(database, play, &fakeCanceler{}, tt.allowTest).VerifyPlayPurchase(aliceCtx(), verifyReq())
			if diff := cmp.Diff(tt.want, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s (err=%v)", diff, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestVerifyPlayPurchaseRequestErrors(t *testing.T) {
	m2m := auth.SetAuthContext(context.Background(), "m2m", "m2m")
	tests := []struct {
		name   string
		ctx    context.Context
		req    *pb.VerifyPlayPurchaseRequest
		getErr error
		want   codes.Code
	}{
		{name: "m2m caller", ctx: m2m, req: verifyReq(), want: codes.PermissionDenied},
		{name: "unauthenticated", ctx: context.Background(), req: verifyReq(), want: codes.Unauthenticated},
		{name: "missing token", ctx: aliceCtx(), req: &pb.VerifyPlayPurchaseRequest{ProductId: playProduct}, want: codes.InvalidArgument},
		{name: "wrong product", ctx: aliceCtx(), req: &pb.VerifyPlayPurchaseRequest{PurchaseToken: playTok, ProductId: "etu_pro"}, want: codes.InvalidArgument},
		{name: "unknown token", ctx: aliceCtx(), req: &pb.VerifyPlayPurchaseRequest{PurchaseToken: "token-bob", ProductId: playProduct}, want: codes.NotFound},
		{name: "play api error", ctx: aliceCtx(), req: verifyReq(), getErr: errors.New("play down"), want: codes.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newBillingMock(t)
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: alicePurchase(stateActive)}, getErr: tt.getErr}
			_, err := newTestBillingService(database, play, &fakeCanceler{}, false).VerifyPlayPurchase(tt.ctx, tt.req)
			if diff := cmp.Diff(tt.want, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s (err=%v)", diff, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestVerifyPlayPurchaseNotConfigured(t *testing.T) {
	_, err := NewBillingService(nil, "").VerifyPlayPurchase(aliceCtx(), verifyReq())
	if diff := cmp.Diff(codes.FailedPrecondition, status.Code(err)); diff != "" {
		t.Errorf("code mismatch (-want +got):\n%s (err=%v)", diff, err)
	}
}

func TestStripeTakeoverCancelsPlay(t *testing.T) {
	tests := []struct {
		name        string
		lapsed      bool
		playState   string
		noPlay      bool
		cancelErr   error
		wantCancels []string
		wantCode    codes.Code
		wantWrite   bool
	}{
		{name: "lapsed play owner is canceled first", lapsed: true, playState: "SUBSCRIPTION_STATE_ON_HOLD", wantCancels: []string{playTok}, wantWrite: true},
		{name: "already canceled play is not canceled again", lapsed: true, playState: stateCancel, wantWrite: true},
		{name: "play cancel failure retries webhook", lapsed: true, playState: stateActive, cancelErr: errors.New("play down"), wantCancels: []string{playTok}, wantCode: codes.Unavailable},
		{name: "play not configured retries webhook", lapsed: true, playState: stateActive, noPlay: true, wantCode: codes.Unavailable},
		{name: "paid play owner is not canceled", wantCode: codes.FailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newBillingMock(t)
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: alicePurchase(tt.playState)}, cancelErr: tt.cancelErr}
			svc := NewAuthService(database)
			if !tt.noPlay {
				svc.SetPlay(play)
			}
			count := 0
			if tt.lapsed {
				count = 1
			}
			mock.ExpectQuery(lapsedPlayQ).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
			if tt.lapsed {
				now := time.Now().UTC()
				mock.ExpectQuery(activePlayQ).WithArgs(acctUser, stateExpired, statePendingCanceled).
					WillReturnRows(sqlmock.NewRows([]string{"purchaseToken", "userId", "productId", "basePlanId", "state", colCreatedAt, colUpdatedAt}).
						AddRow(playTok, acctUser, playProduct, "yearly", stateActive, now, now))
			}
			if tt.wantWrite {
				mock.ExpectBegin()
				mock.ExpectExec(stripeUpdate).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				expectBillingUser(mock, "active", provStripe)
			}
			if tt.wantCode == codes.FailedPrecondition {
				mock.ExpectBegin()
				mock.ExpectExec(stripeUpdate).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectCommit()
				expectBillingUser(mock, "active", provPlay)
			}

			m2m := auth.SetAuthContext(context.Background(), "m2m", "m2m")
			_, err := svc.UpdateUserSubscription(m2m, &pb.UpdateUserSubscriptionRequest{UserId: acctUser, SubscriptionStatus: "active"})
			if diff := cmp.Diff(tt.wantCode, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s (err=%v)", diff, err)
			}
			if diff := cmp.Diff(tt.wantCancels, play.cancels); diff != "" {
				t.Errorf("play cancels mismatch (-want +got):\n%s", diff)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestStripeNonPayingStatusSkipsPlayCheck(t *testing.T) {
	database, mock := newBillingMock(t)
	play := &fakePlay{}
	svc := NewAuthService(database)
	svc.SetPlay(play)
	mock.ExpectBegin()
	mock.ExpectExec(stripeUpdate).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	expectBillingUser(mock, "canceled", provStripe)

	m2m := auth.SetAuthContext(context.Background(), "m2m", "m2m")
	if _, err := svc.UpdateUserSubscription(m2m, &pb.UpdateUserSubscriptionRequest{UserId: acctUser, SubscriptionStatus: "canceled"}); err != nil {
		t.Fatalf("UpdateUserSubscription: %v", err)
	}
	if len(play.cancels) != 0 {
		t.Errorf("play cancels = %v, want none", play.cancels)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}
