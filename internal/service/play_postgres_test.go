package service

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/billing"
	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type billingState struct {
	Status   string
	Provider string
}

func currentBilling(t *testing.T, s *AuthService, id string) billingState {
	t.Helper()
	user, err := s.db.GetUser(t.Context(), id)
	if err != nil || user == nil {
		t.Fatalf("GetUser = %v, %v", user, err)
	}
	return billingState{Status: user.SubscriptionStatus, Provider: deref(user.BillingProvider)}
}

func TestPlayPostgresStoreSwitchKeepsStripe(t *testing.T) {
	for _, recovered := range []string{stateActive, stateCancel} {
		t.Run(recovered, func(t *testing.T) {
			s, _ := stripeTestService(t)
			id := stripeTestUser(t.Context(), t, s)
			token := "token-" + id
			purchase := alicePurchase(stateActive)
			purchase.ObfuscatedAccountID = id
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{token: purchase}}
			stripe := &fakeCanceler{}
			s.SetPlay(play)
			sync := &PlaySync{DB: s.db, Play: play, Stripe: stripe, Grace: time.Hour}
			userCtx := auth.SetAuthContext(t.Context(), id, "apikey")
			m2m := stripeContext(t)

			if _, err := NewBillingService(sync, "com.timeclimbers.etu").VerifyPlayPurchase(userCtx, &pb.VerifyPlayPurchaseRequest{PurchaseToken: token, ProductId: playProduct}); err != nil {
				t.Fatalf("VerifyPlayPurchase: %v", err)
			}
			play.purchases[token].State = "SUBSCRIPTION_STATE_ON_HOLD"
			if err := sync.HandleNotification(t.Context(), token, false); err != nil {
				t.Fatalf("on hold notification: %v", err)
			}
			if diff := cmp.Diff(billingState{Status: "unpaid", Provider: provPlay}, currentBilling(t, s, id)); diff != "" {
				t.Fatalf("after hold (-want +got):\n%s", diff)
			}

			customer := "cus_" + id
			if _, err := s.UpdateUserSubscription(m2m, &pb.UpdateUserSubscriptionRequest{UserId: id, SubscriptionStatus: "unpaid", StripeCustomerId: &customer}); err != nil {
				t.Fatalf("checkout link: %v", err)
			}
			if diff := cmp.Diff(billingState{Status: "unpaid", Provider: provPlay}, currentBilling(t, s, id)); diff != "" {
				t.Fatalf("unpaid checkout write took ownership (-want +got):\n%s", diff)
			}

			event := "evt_active_" + id
			sub := "sub_" + id
			if _, err := s.UpdateUserSubscription(m2m, &pb.UpdateUserSubscriptionRequest{
				UserId: id, SubscriptionStatus: "active", StripeSubscriptionId: &sub, StripeEventId: &event, ExpectedStripeCustomerId: &customer,
			}); err != nil {
				t.Fatalf("active webhook: %v", err)
			}
			if diff := cmp.Diff([]string{token}, play.cancels); diff != "" {
				t.Errorf("play cancels at the active webhook (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(billingState{Status: "active", Provider: provStripe}, currentBilling(t, s, id)); diff != "" {
				t.Fatalf("after stripe activation (-want +got):\n%s", diff)
			}

			play.purchases[token].State = recovered
			if err := sync.HandleNotification(t.Context(), token, false); err != nil {
				t.Fatalf("recovery notification: %v", err)
			}
			if len(stripe.calls) != 0 {
				t.Errorf("stripe cancels = %v, want none", stripe.calls)
			}
			if diff := cmp.Diff(billingState{Status: "active", Provider: provStripe}, currentBilling(t, s, id)); diff != "" {
				t.Errorf("after play recovery (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPlayPostgresStaleStripeEventKeepsPlay(t *testing.T) {
	tests := []struct {
		name      string
		duplicate bool
		stale     bool
		wantCode  codes.Code
	}{
		{name: "duplicate event", duplicate: true},
		{name: "stale customer link", stale: true, wantCode: codes.FailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := stripeTestService(t)
			id := stripeTestUser(t.Context(), t, s)
			token := "token-" + id
			purchase := alicePurchase(stateActive)
			purchase.ObfuscatedAccountID = id
			play := &fakePlay{purchases: map[string]*billing.PlayPurchase{token: purchase}}
			s.SetPlay(play)
			sync := &PlaySync{DB: s.db, Play: play, Grace: time.Hour}
			userCtx := auth.SetAuthContext(t.Context(), id, "apikey")
			m2m := stripeContext(t)
			if _, err := NewBillingService(sync, "com.timeclimbers.etu").VerifyPlayPurchase(userCtx, &pb.VerifyPlayPurchaseRequest{PurchaseToken: token, ProductId: playProduct}); err != nil {
				t.Fatalf("VerifyPlayPurchase: %v", err)
			}
			play.purchases[token].State = "SUBSCRIPTION_STATE_ON_HOLD"
			if err := sync.HandleNotification(t.Context(), token, false); err != nil {
				t.Fatal(err)
			}
			customer := "cus_" + id
			event := "evt_" + id
			if _, err := s.UpdateUserSubscription(m2m, &pb.UpdateUserSubscriptionRequest{UserId: id, SubscriptionStatus: "unpaid", StripeCustomerId: &customer, StripeEventId: proto.String(event)}); err != nil {
				t.Fatal(err)
			}

			req := &pb.UpdateUserSubscriptionRequest{UserId: id, SubscriptionStatus: "active", ExpectedStripeCustomerId: &customer}
			if tt.duplicate {
				req.StripeEventId = proto.String(event)
			}
			if tt.stale {
				req.ExpectedStripeCustomerId = proto.String("cus_old_" + id)
				req.StripeEventId = proto.String("evt_stale_" + id)
			}
			_, err := s.UpdateUserSubscription(m2m, req)
			if diff := cmp.Diff(tt.wantCode, status.Code(err)); diff != "" {
				t.Errorf("code (-want +got):\n%s (err=%v)", diff, err)
			}
			if len(play.cancels) != 0 {
				t.Errorf("play cancels = %v, want none", play.cancels)
			}
			if diff := cmp.Diff(billingState{Status: "unpaid", Provider: provPlay}, currentBilling(t, s, id)); diff != "" {
				t.Errorf("user (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPlayPostgresNotificationLeavesStripeSubscriber(t *testing.T) {
	s, _ := stripeTestService(t)
	id := stripeTestUser(t.Context(), t, s)
	m2m := stripeContext(t)
	customer, sub := "cus_"+id, "sub_"+id
	if _, err := s.UpdateUserSubscription(m2m, &pb.UpdateUserSubscriptionRequest{UserId: id, SubscriptionStatus: "active", StripeCustomerId: &customer, StripeSubscriptionId: &sub}); err != nil {
		t.Fatal(err)
	}
	token := "token-mallory-" + id
	purchase := alicePurchase(stateActive)
	purchase.ObfuscatedAccountID = id
	play := &fakePlay{purchases: map[string]*billing.PlayPurchase{token: purchase}}
	stripe := &fakeCanceler{}
	sync := &PlaySync{DB: s.db, Play: play, Stripe: stripe, Grace: time.Hour}

	if err := sync.HandleNotification(t.Context(), token, false); err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	if len(stripe.calls) != 0 || len(play.acks) != 0 {
		t.Errorf("stripe cancels = %v, acks = %v, want none", stripe.calls, play.acks)
	}
	if diff := cmp.Diff(billingState{Status: "active", Provider: provStripe}, currentBilling(t, s, id)); diff != "" {
		t.Errorf("user (-want +got):\n%s", diff)
	}
	if owner, err := s.db.PlaySubscriptionUser(t.Context(), token); err != nil || owner != id {
		t.Errorf("recorded token owner = %q, %v, want %q", owner, err, id)
	}
}
