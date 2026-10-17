package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"go.icco.me/etu-backend/internal/billing"
)

const (
	voidSelectQ      = `SELECT "userId","superseded" FROM "PlaySubscription" WHERE "purchaseToken" = \$1`
	voidRowQ         = `UPDATE "PlaySubscription" SET "autoRenewing"=\$1,"state"=\$2,"updatedAt"=\$3 WHERE "purchaseToken" = \$4`
	otherActivePlayQ = `SELECT count\(\*\) FROM "PlaySubscription" WHERE .*"purchaseToken" <> \$2`
	voidUpdateQ      = `UPDATE "User" SET "cancelAtPeriodEnd"=\$1,"pastDueSince"=NULL,"subscriptionStatus"=\$2,"updatedAt"=\$3 WHERE id = \$4`
)

func expectVoidLookup(m sqlmock.Sqlmock, superseded bool) {
	m.ExpectBegin()
	m.ExpectQuery(voidSelectQ).WithArgs(playTok, 1).WillReturnRows(sqlmock.NewRows([]string{"userId", "superseded"}).AddRow(acctUser, superseded))
}

func expectVoidRow(m sqlmock.Sqlmock) {
	m.ExpectExec(voidRowQ).WithArgs(false, stateExpired, sqlmock.AnyArg(), playTok).WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectNoOtherActive(m sqlmock.Sqlmock, n int) {
	m.ExpectQuery(otherActivePlayQ).WithArgs(acctUser, playTok, stateActive, stateCancel, stateGrace).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(n))
}

func TestHandleNotificationSyncsSubscription(t *testing.T) {
	database, mock := newBillingMock(t)
	play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: alicePurchase(stateActive)}}
	expectTokenOwner(mock, acctUser)
	expectBillingUser(mock, "trialing", provPlay)
	expectPlayApply(mock, "trialing", 1)
	expectMarkAck(mock)
	sync := &PlaySync{DB: database, Play: play, Grace: time.Hour}

	if err := sync.HandleNotification(context.Background(), playTok, false); err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestHandleNotificationPermanentErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*billing.PlayPurchase, *fakePlay, sqlmock.Sqlmock)
		want  error
	}{
		{
			name: "purchase unknown to play",
			setup: func(_ *billing.PlayPurchase, p *fakePlay, _ sqlmock.Sqlmock) {
				p.purchases = map[string]*billing.PlayPurchase{}
			},
			want: billing.ErrPlayPurchaseNotFound,
		},
		{
			name:  "wrong product",
			setup: func(p *billing.PlayPurchase, _ *fakePlay, _ sqlmock.Sqlmock) { p.ProductID = "etu_lifetime" },
			want:  ErrPlayWrongProduct,
		},
		{
			name:  "test purchase",
			setup: func(p *billing.PlayPurchase, _ *fakePlay, _ sqlmock.Sqlmock) { p.Test = true },
			want:  ErrPlayTestPurchase,
		},
		{
			name: "no matching user",
			setup: func(p *billing.PlayPurchase, _ *fakePlay, m sqlmock.Sqlmock) {
				p.ObfuscatedAccountID = ""
				expectTokenOwner(m, "")
			},
			want: ErrPlayUnmatched,
		},
		{
			name: "user deleted",
			setup: func(_ *billing.PlayPurchase, _ *fakePlay, m sqlmock.Sqlmock) {
				expectTokenOwner(m, acctUser)
				m.ExpectQuery(selectUserQ).WithArgs(acctUser, 1).WillReturnRows(sqlmock.NewRows(billingUserColumns))
			},
			want: ErrPlayUnmatched,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newBillingMock(t)
			p := alicePurchase(stateActive)
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: p}}
			tt.setup(p, play, mock)
			sync := &PlaySync{DB: database, Play: play, Grace: time.Hour}

			err := sync.HandleNotification(context.Background(), playTok, false)
			if !errors.Is(err, billing.ErrRTDNPermanent) {
				t.Errorf("err = %v, want it to wrap %v", err, billing.ErrRTDNPermanent)
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want it to wrap %v", err, tt.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestHandleNotificationTransientErrors(t *testing.T) {
	boom := errors.New("play unavailable")
	t.Run("play api failure", func(t *testing.T) {
		database, mock := newBillingMock(t)
		sync := &PlaySync{DB: database, Play: &fakePlay{getErr: boom}, Grace: time.Hour}
		err := sync.HandleNotification(context.Background(), playTok, false)
		if !errors.Is(err, boom) || errors.Is(err, billing.ErrRTDNPermanent) {
			t.Errorf("err = %v, want transient %v", err, boom)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled mock expectations: %v", err)
		}
	})
}

func TestHandleNotificationLeavesStripeSubscriber(t *testing.T) {
	tests := []struct {
		name        string
		storedOwner string
		userStatus  string
	}{
		{name: "unseen token tagged with a paid stripe subscriber", userStatus: "active"},
		{name: "unseen token tagged with a lapsed stripe subscriber", userStatus: "canceled"},
		{name: "stored token of a stripe subscriber", storedOwner: acctUser, userStatus: "active"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newBillingMock(t)
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: alicePurchase(stateActive)}}
			stripe := &fakeCanceler{}
			expectTokenOwner(mock, tt.storedOwner)
			expectBillingUser(mock, tt.userStatus, provStripe)
			expectPlayRecord(mock, tt.userStatus, provStripe)
			sync := &PlaySync{DB: database, Play: play, Stripe: stripe, Grace: time.Hour}

			if err := sync.HandleNotification(context.Background(), playTok, false); err != nil {
				t.Fatalf("HandleNotification: %v", err)
			}
			if len(stripe.calls) != 0 || len(play.acks) != 0 {
				t.Errorf("stripe calls = %v, acks = %v, want none", stripe.calls, play.acks)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestHandleNotificationVoided(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr error
		wantOK  bool
	}{
		{
			name: "forces the owner to canceled",
			setup: func(m sqlmock.Sqlmock) {
				expectVoidLookup(m, false)
				expectVoidRow(m)
				expectNoOtherActive(m, 0)
				m.ExpectExec(voidUpdateQ).
					WithArgs(false, "canceled", sqlmock.AnyArg(), acctUser, provPlay, "active", "trial", "trialing", "pro", "premium", sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectCommit()
				expectBillingUser(m, "canceled", provPlay)
			},
			wantOK: true,
		},
		{
			name: "other active play subscription keeps the user paid",
			setup: func(m sqlmock.Sqlmock) {
				expectVoidLookup(m, false)
				expectVoidRow(m)
				expectNoOtherActive(m, 1)
				m.ExpectCommit()
				expectBillingUser(m, "active", provPlay)
			},
			wantOK: true,
		},
		{
			name: "unknown token is permanent",
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectBegin()
				m.ExpectQuery(voidSelectQ).WithArgs(playTok, 1).WillReturnRows(sqlmock.NewRows([]string{"userId", "superseded"}))
				m.ExpectRollback()
			},
			wantErr: billing.ErrRTDNPermanent,
		},
		{
			name: "stripe owning a paid subscription is acknowledged",
			setup: func(m sqlmock.Sqlmock) {
				expectVoidLookup(m, false)
				expectVoidRow(m)
				expectNoOtherActive(m, 0)
				m.ExpectExec(voidUpdateQ).WillReturnResult(sqlmock.NewResult(0, 0))
				m.ExpectCommit()
			},
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, mock := newBillingMock(t)
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{playTok: alicePurchase(stateActive)}}
			tt.setup(mock)
			sync := &PlaySync{DB: database, Play: play, Grace: time.Hour}

			err := sync.HandleNotification(context.Background(), playTok, true)
			if tt.wantOK && err != nil {
				t.Fatalf("HandleNotification: %v", err)
			}
			if !tt.wantOK && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if len(play.acks) != 0 || len(play.cancels) != 0 {
				t.Errorf("acks = %v, cancels = %v, want none", play.acks, play.cancels)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}
