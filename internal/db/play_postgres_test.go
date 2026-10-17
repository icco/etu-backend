package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.icco.me/etu-backend/internal/models"
)

func playPostgres(t *testing.T) (*DB, *sql.DB) {
	t.Helper()
	url := os.Getenv("STRIPE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("STRIPE_TEST_DATABASE_URL is not set")
	}
	conn, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	d, err := NewFromConn(conn)
	if err != nil {
		t.Fatal(err)
	}
	d.SetPaywallGrace(time.Hour)
	return d, conn
}

// pgUser creates a user with the given billing state. pastDueAgo, when set,
// is how long ago the user became past due.
func pgUser(ctx context.Context, t *testing.T, d *DB, conn *sql.DB, status, provider string, pastDueAgo time.Duration) string {
	t.Helper()
	user, err := d.CreateUser(ctx, fmt.Sprintf("ada-%s@example.com", models.GenerateCUID()), "hash")
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.ExecContext(ctx, `UPDATE "User" SET "subscriptionStatus" = NULLIF($1, ''), "billingProvider" = NULLIF($2, ''),
		"pastDueSince" = CASE WHEN $3::float8 > 0 THEN now() - make_interval(secs => $3::float8) END WHERE id = $4`,
		status, provider, pastDueAgo.Seconds(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return user.ID
}

type pgBilling struct {
	Status   string
	Provider string
}

func pgUserBilling(ctx context.Context, t *testing.T, d *DB, id string) pgBilling {
	t.Helper()
	user, err := d.GetUser(ctx, id)
	if err != nil || user == nil {
		t.Fatalf("GetUser(%s) = %v, %v", id, user, err)
	}
	return pgBilling{Status: user.SubscriptionStatus, Provider: deref(user.BillingProvider)}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type pgPlayRow struct {
	UserID       string
	State        string
	Acknowledged bool
	Superseded   bool
	AutoRenewing bool
}

func pgPlay(ctx context.Context, t *testing.T, conn *sql.DB, token string) pgPlayRow {
	t.Helper()
	var r pgPlayRow
	err := conn.QueryRowContext(ctx, `SELECT "userId", "state", "acknowledged", "superseded", "autoRenewing" FROM "PlaySubscription" WHERE "purchaseToken" = $1`, token).
		Scan(&r.UserID, &r.State, &r.Acknowledged, &r.Superseded, &r.AutoRenewing)
	if err != nil {
		t.Fatalf("read play subscription %s: %v", token, err)
	}
	return r
}

func pgToken() string {
	return "token-" + models.GenerateCUID()
}

func playUpdate(token, userID, state, status string) PlaySubscriptionUpdate {
	expiry := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	return PlaySubscriptionUpdate{
		PurchaseToken: token, UserID: userID, ProductID: "etu", BasePlanID: "yearly",
		State: state, ExpiryTime: &expiry, AutoRenewing: true, Status: status,
	}
}

func TestPlayPostgresApplyUpsert(t *testing.T) {
	d, conn := playPostgres(t)
	ctx := t.Context()
	id := pgUser(ctx, t, d, conn, "free", "", 0)
	first, second := pgToken(), pgToken()

	user, err := d.ApplyPlaySubscription(ctx, playUpdate(first, id, playStateActive, "trialing"), time.Hour)
	if err != nil || user == nil {
		t.Fatalf("first apply: %v, %v", user, err)
	}
	if diff := cmp.Diff(pgBilling{Status: "trialing", Provider: BillingProviderPlay}, pgUserBilling(ctx, t, d, id)); diff != "" {
		t.Errorf("user after purchase (-want +got):\n%s", diff)
	}

	again := playUpdate(first, id, playStateCanc, "active")
	again.CancelAtPeriodEnd, again.Acknowledged, again.AutoRenewing = true, true, false
	if _, err := d.ApplyPlaySubscription(ctx, again, time.Hour); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if diff := cmp.Diff(pgPlayRow{UserID: id, State: playStateCanc, Acknowledged: true}, pgPlay(ctx, t, conn, first)); diff != "" {
		t.Errorf("row after upsert (-want +got):\n%s", diff)
	}
	if user, err := d.GetUser(ctx, id); err != nil || !user.CancelAtPeriodEnd || user.SubscriptionStatus != "active" {
		t.Errorf("user after cancel = %+v, %v", user, err)
	}

	linked := playUpdate(second, id, playStateActive, "active")
	linked.LinkedPurchaseToken = &first
	if _, err := d.ApplyPlaySubscription(ctx, linked, time.Hour); err != nil {
		t.Fatalf("linked apply: %v", err)
	}
	if !pgPlay(ctx, t, conn, first).Superseded {
		t.Error("linked token was not superseded")
	}
	if _, err := d.ApplyPlaySubscription(ctx, playUpdate(first, id, playStateVoid, "canceled"), time.Hour); err != nil {
		t.Fatalf("superseded apply: %v", err)
	}
	if diff := cmp.Diff(pgBilling{Status: "active", Provider: BillingProviderPlay}, pgUserBilling(ctx, t, d, id)); diff != "" {
		t.Errorf("superseded token changed the user (-want +got):\n%s", diff)
	}
}

func TestPlayPostgresTokenOwnerConflict(t *testing.T) {
	d, conn := playPostgres(t)
	ctx := t.Context()
	alice := pgUser(ctx, t, d, conn, "free", "", 0)
	bob := pgUser(ctx, t, d, conn, "free", "", 0)
	token := pgToken()

	if _, err := d.ApplyPlaySubscription(ctx, playUpdate(token, alice, playStateActive, "trialing"), time.Hour); err != nil {
		t.Fatal(err)
	}
	user, err := d.ApplyPlaySubscription(ctx, playUpdate(token, bob, playStateActive, "trialing"), time.Hour)
	if !errors.Is(err, ErrPlayTokenUserMismatch) || user != nil {
		t.Fatalf("bob apply = %v, %v, want %v", user, err, ErrPlayTokenUserMismatch)
	}
	if got := pgPlay(ctx, t, conn, token).UserID; got != alice {
		t.Errorf("token owner = %s, want alice %s", got, alice)
	}
	if diff := cmp.Diff(pgBilling{Status: "free"}, pgUserBilling(ctx, t, d, bob)); diff != "" {
		t.Errorf("bob changed (-want +got):\n%s", diff)
	}
}

func TestPlayPostgresUnpaidStatusRespectsOtherPurchase(t *testing.T) {
	d, conn := playPostgres(t)
	ctx := t.Context()
	id := pgUser(ctx, t, d, conn, "free", "", 0)
	old, current := pgToken(), pgToken()
	if _, err := d.ApplyPlaySubscription(ctx, playUpdate(old, id, playStateActive, "active"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ApplyPlaySubscription(ctx, playUpdate(current, id, playStateActive, "active"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ApplyPlaySubscription(ctx, playUpdate(old, id, playStateVoid, "canceled"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(pgBilling{Status: "active", Provider: BillingProviderPlay}, pgUserBilling(ctx, t, d, id)); diff != "" {
		t.Errorf("expired old token changed the user (-want +got):\n%s", diff)
	}
	if _, err := d.ApplyPlaySubscription(ctx, playUpdate(current, id, playStateVoid, "canceled"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(pgBilling{Status: "canceled", Provider: BillingProviderPlay}, pgUserBilling(ctx, t, d, id)); diff != "" {
		t.Errorf("last expiry did not cancel the user (-want +got):\n%s", diff)
	}
}

func TestPlayPostgresVoid(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		provider   string
		others     int
		superseded bool
		wantErr    error
		want       pgBilling
	}{
		{name: "voids the only purchase", status: "active", provider: BillingProviderPlay, want: pgBilling{Status: "canceled", Provider: BillingProviderPlay}},
		{name: "other active purchase keeps the user paid", status: "active", provider: BillingProviderPlay, others: 1, want: pgBilling{Status: "active", Provider: BillingProviderPlay}},
		{name: "superseded purchase leaves the user", status: "active", provider: BillingProviderPlay, superseded: true, want: pgBilling{Status: "active", Provider: BillingProviderPlay}},
		{name: "paid stripe subscriber is untouched", status: "active", provider: BillingProviderStripe, wantErr: ErrBillingProviderMismatch, want: pgBilling{Status: "active", Provider: BillingProviderStripe}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, conn := playPostgres(t)
			ctx := t.Context()
			id := pgUser(ctx, t, d, conn, tt.status, tt.provider, 0)
			token := pgToken()
			if _, err := d.ApplyPlaySubscription(ctx, playUpdate(token, id, playStateActive, ""), time.Hour); err != nil {
				t.Fatal(err)
			}
			for range tt.others {
				if _, err := d.ApplyPlaySubscription(ctx, playUpdate(pgToken(), id, playStateActive, ""), time.Hour); err != nil {
					t.Fatal(err)
				}
			}
			if tt.superseded {
				if _, err := conn.ExecContext(ctx, `UPDATE "PlaySubscription" SET "superseded" = true WHERE "purchaseToken" = $1`, token); err != nil {
					t.Fatal(err)
				}
			}

			_, err := d.VoidPlaySubscription(ctx, token, time.Hour)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if row := pgPlay(ctx, t, conn, token); row.State != playStateVoid || row.AutoRenewing {
				t.Errorf("voided row = %+v, want expired and not renewing", row)
			}
			if diff := cmp.Diff(tt.want, pgUserBilling(ctx, t, d, id)); diff != "" {
				t.Errorf("user (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("unknown token", func(t *testing.T) {
		d, _ := playPostgres(t)
		if _, err := d.VoidPlaySubscription(t.Context(), pgToken(), time.Hour); !errors.Is(err, ErrPlaySubscriptionNotFound) {
			t.Errorf("err = %v, want %v", err, ErrPlaySubscriptionNotFound)
		}
	})
}

func TestPlayPostgresConcurrentVoids(t *testing.T) {
	d, conn := playPostgres(t)
	ctx := t.Context()
	for range 10 {
		id := pgUser(ctx, t, d, conn, "free", "", 0)
		tokens := []string{pgToken(), pgToken()}
		for _, token := range tokens {
			if _, err := d.ApplyPlaySubscription(ctx, playUpdate(token, id, playStateActive, "active"), time.Hour); err != nil {
				t.Fatal(err)
			}
		}
		errs := make([]error, len(tokens))
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i, token := range tokens {
			wg.Go(func() {
				<-start
				_, errs[i] = d.VoidPlaySubscription(ctx, token, time.Hour)
			})
		}
		close(start)
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if diff := cmp.Diff(pgBilling{Status: "canceled", Provider: BillingProviderPlay}, pgUserBilling(ctx, t, d, id)); diff != "" {
			t.Fatalf("user kept access after both purchases were voided (-want +got):\n%s", diff)
		}
	}
}

func TestPlayPostgresOwnership(t *testing.T) {
	type outcome struct {
		Applied  bool
		Provider string
	}
	states := []struct {
		name       string
		status     string
		provider   string
		pastDueAgo time.Duration
		stripePaid outcome
		stripeOff  outcome
		playPaid   outcome
		playOff    outcome
	}{
		{
			name: "unowned unpaid", status: "free",
			stripePaid: outcome{true, BillingProviderStripe}, stripeOff: outcome{true, ""},
			playPaid: outcome{true, BillingProviderPlay}, playOff: outcome{true, ""},
		},
		{
			name: "unowned paid", status: "pro",
			stripePaid: outcome{true, BillingProviderStripe}, stripeOff: outcome{true, ""},
			playPaid: outcome{true, BillingProviderPlay}, playOff: outcome{false, ""},
		},
		{
			name: "stripe paid", status: "active", provider: BillingProviderStripe,
			stripePaid: outcome{true, BillingProviderStripe}, stripeOff: outcome{true, BillingProviderStripe},
			playPaid: outcome{false, BillingProviderStripe}, playOff: outcome{false, BillingProviderStripe},
		},
		{
			name: "stripe past_due in grace", status: SubscriptionStatusPastDue, provider: BillingProviderStripe, pastDueAgo: 10 * time.Minute,
			stripePaid: outcome{true, BillingProviderStripe}, stripeOff: outcome{true, BillingProviderStripe},
			playPaid: outcome{false, BillingProviderStripe}, playOff: outcome{false, BillingProviderStripe},
		},
		{
			name: "stripe past_due out of grace", status: SubscriptionStatusPastDue, provider: BillingProviderStripe, pastDueAgo: 2 * time.Hour,
			stripePaid: outcome{true, BillingProviderStripe}, stripeOff: outcome{true, BillingProviderStripe},
			playPaid: outcome{true, BillingProviderPlay}, playOff: outcome{false, BillingProviderStripe},
		},
		{
			name: "stripe lapsed", status: "canceled", provider: BillingProviderStripe,
			stripePaid: outcome{true, BillingProviderStripe}, stripeOff: outcome{true, BillingProviderStripe},
			playPaid: outcome{true, BillingProviderPlay}, playOff: outcome{false, BillingProviderStripe},
		},
		{
			name: "play paid", status: "trialing", provider: BillingProviderPlay,
			stripePaid: outcome{false, BillingProviderPlay}, stripeOff: outcome{false, BillingProviderPlay},
			playPaid: outcome{true, BillingProviderPlay}, playOff: outcome{true, BillingProviderPlay},
		},
		{
			name: "play past_due in grace", status: SubscriptionStatusPastDue, provider: BillingProviderPlay, pastDueAgo: 10 * time.Minute,
			stripePaid: outcome{false, BillingProviderPlay}, stripeOff: outcome{false, BillingProviderPlay},
			playPaid: outcome{true, BillingProviderPlay}, playOff: outcome{true, BillingProviderPlay},
		},
		{
			name: "play lapsed", status: "unpaid", provider: BillingProviderPlay,
			stripePaid: outcome{true, BillingProviderStripe}, stripeOff: outcome{true, BillingProviderPlay},
			playPaid: outcome{true, BillingProviderPlay}, playOff: outcome{true, BillingProviderPlay},
		},
	}
	writes := []struct {
		name   string
		status string
		want   func(i int) outcome
		write  func(ctx context.Context, d *DB, id string) error
	}{
		{
			name: "stripe active", status: "active", want: func(i int) outcome { return states[i].stripePaid },
			write: func(ctx context.Context, d *DB, id string) error {
				_, err := d.UpdateUserSubscriptionCancelingPlay(ctx, id, SubscriptionUpdate{SubscriptionStatus: "active"}, func(context.Context, []PlaySubscription) error { return nil })
				return err
			},
		},
		{
			name: "stripe incomplete", status: "incomplete", want: func(i int) outcome { return states[i].stripeOff },
			write: func(ctx context.Context, d *DB, id string) error {
				_, err := d.UpdateUserSubscription(ctx, id, SubscriptionUpdate{SubscriptionStatus: "incomplete"})
				return err
			},
		},
		{
			name: "play trialing", status: "trialing", want: func(i int) outcome { return states[i].playPaid },
			write: func(ctx context.Context, d *DB, id string) error {
				_, err := d.ApplyPlaySubscription(ctx, playUpdate(pgToken(), id, playStateActive, "trialing"), time.Hour)
				return err
			},
		},
		{
			name: "play on hold", status: "unpaid", want: func(i int) outcome { return states[i].playOff },
			write: func(ctx context.Context, d *DB, id string) error {
				_, err := d.ApplyPlaySubscription(ctx, playUpdate(pgToken(), id, "SUBSCRIPTION_STATE_ON_HOLD", "unpaid"), time.Hour)
				return err
			},
		},
	}
	d, conn := playPostgres(t)
	for i, state := range states {
		for _, w := range writes {
			t.Run(state.name+"/"+w.name, func(t *testing.T) {
				ctx := t.Context()
				id := pgUser(ctx, t, d, conn, state.status, state.provider, state.pastDueAgo)
				want := w.want(i)
				err := w.write(ctx, d, id)
				switch {
				case want.Applied && err != nil:
					t.Fatalf("write refused: %v", err)
				case !want.Applied && !errors.Is(err, ErrBillingProviderMismatch):
					t.Fatalf("err = %v, want %v", err, ErrBillingProviderMismatch)
				}
				wantStatus := state.status
				if want.Applied {
					wantStatus = w.status
				}
				if diff := cmp.Diff(pgBilling{Status: wantStatus, Provider: want.Provider}, pgUserBilling(ctx, t, d, id)); diff != "" {
					t.Errorf("user (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func TestPlayPostgresStripeCancelOnlyWhenWriteApplies(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		provider   string
		customer   string
		expected   string
		eventSeen  bool
		wantCancel bool
		wantErr    error
	}{
		{name: "lapsed play owner", status: "unpaid", provider: BillingProviderPlay, wantCancel: true},
		{name: "stripe owner with a stray play purchase", status: "unpaid", provider: BillingProviderStripe, wantCancel: true},
		{name: "paid play owner", status: "trialing", provider: BillingProviderPlay, wantErr: ErrBillingProviderMismatch},
		{name: "duplicate event", status: "unpaid", provider: BillingProviderPlay, eventSeen: true},
		{name: "stale customer link", status: "unpaid", provider: BillingProviderPlay, customer: "cus_new", expected: "cus_old", wantErr: ErrStripeCustomerMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, conn := playPostgres(t)
			ctx := t.Context()
			id := pgUser(ctx, t, d, conn, tt.status, tt.provider, 0)
			if tt.customer != "" {
				if _, err := conn.ExecContext(ctx, `UPDATE "User" SET "stripeCustomerId" = $1 WHERE id = $2`, tt.customer, id); err != nil {
					t.Fatal(err)
				}
			}
			token := pgToken()
			if _, err := d.ApplyPlaySubscription(ctx, playUpdate(token, id, "SUBSCRIPTION_STATE_ON_HOLD", ""), time.Hour); err != nil {
				t.Fatal(err)
			}
			event := "evt_" + id
			if tt.eventSeen {
				if _, err := conn.ExecContext(ctx, `INSERT INTO "StripeEvent" (id, "createdAt") VALUES ($1, now())`, event); err != nil {
					t.Fatal(err)
				}
			}

			var cancels []string
			_, err := d.UpdateUserSubscriptionCancelingPlay(ctx, id, SubscriptionUpdate{SubscriptionStatus: "active", StripeEventID: event, ExpectedStripeCustomerID: tt.expected},
				func(_ context.Context, subs []PlaySubscription) error {
					for _, s := range subs {
						cancels = append(cancels, s.PurchaseToken)
					}
					return nil
				})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			var want []string
			if tt.wantCancel {
				want = []string{token}
			}
			if diff := cmp.Diff(want, cancels); diff != "" {
				t.Errorf("play cancels (-want +got):\n%s", diff)
			}
		})
	}
}
