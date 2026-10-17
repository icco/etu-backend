package billing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/api/option"
)

func TestMapPlayStatus(t *testing.T) {
	tests := []struct {
		name       string
		purchase   *PlayPurchase
		wantStatus string
		wantCancel bool
	}{
		{"active", &PlayPurchase{State: "SUBSCRIPTION_STATE_ACTIVE"}, "active", false},
		{"active free trial", &PlayPurchase{State: "SUBSCRIPTION_STATE_ACTIVE", FreeTrial: true}, "trialing", false},
		{"canceled", &PlayPurchase{State: "SUBSCRIPTION_STATE_CANCELED"}, "active", true},
		{"canceled free trial", &PlayPurchase{State: "SUBSCRIPTION_STATE_CANCELED", FreeTrial: true}, "active", true},
		{"in grace period", &PlayPurchase{State: "SUBSCRIPTION_STATE_IN_GRACE_PERIOD"}, "past_due", false},
		{"on hold", &PlayPurchase{State: "SUBSCRIPTION_STATE_ON_HOLD"}, "unpaid", false},
		{"paused", &PlayPurchase{State: "SUBSCRIPTION_STATE_PAUSED"}, "unpaid", false},
		{"expired", &PlayPurchase{State: "SUBSCRIPTION_STATE_EXPIRED"}, "canceled", false},
		{"pending purchase canceled", &PlayPurchase{State: "SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED"}, "canceled", false},
		{"pending", &PlayPurchase{State: "SUBSCRIPTION_STATE_PENDING"}, "", false},
		{"unspecified", &PlayPurchase{State: "SUBSCRIPTION_STATE_UNSPECIFIED"}, "", false},
		{"empty", &PlayPurchase{}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotCancel := MapPlayStatus(tt.purchase)
			if gotStatus != tt.wantStatus || gotCancel != tt.wantCancel {
				t.Errorf("MapPlayStatus() = (%q, %v), want (%q, %v)", gotStatus, gotCancel, tt.wantStatus, tt.wantCancel)
			}
		})
	}
}

const adaPurchaseJSON = `{
  "kind": "androidpublisher#subscriptionPurchaseV2",
  "subscriptionState": "SUBSCRIPTION_STATE_ACTIVE",
  "acknowledgementState": "ACKNOWLEDGEMENT_STATE_PENDING",
  "linkedPurchaseToken": "old-token",
  "externalAccountIdentifiers": {"obfuscatedExternalAccountId": "ada-lovelace-account"},
  "testPurchase": {},
  "lineItems": [
    {
      "productId": "etu",
      "expiryTime": "2026-11-01T10:00:00Z",
      "autoRenewingPlan": {"autoRenewEnabled": true},
      "offerDetails": {"basePlanId": "monthly"},
      "offerPhase": {"freeTrial": {}}
    },
    {
      "productId": "addon",
      "expiryTime": "2026-12-01T10:00:00.5Z",
      "offerDetails": {"basePlanId": "yearly"}
    }
  ]
}`

func newTestPlay(t *testing.T, handler http.HandlerFunc) *Play {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p, err := newPlayWithOptions(context.Background(), "com.timeclimbers.etu",
		option.WithEndpoint(srv.URL), option.WithoutAuthentication(), option.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("newPlayWithOptions: %v", err)
	}
	return p
}

func TestPlayGetSubscriptionParses(t *testing.T) {
	var gotPath string
	p := newTestPlay(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, adaPurchaseJSON)
	})

	got, err := p.GetSubscription(context.Background(), "tok-ada")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.timeclimbers.etu/purchases/subscriptionsv2/tokens/tok-ada"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	exp := time.Date(2026, 12, 1, 10, 0, 0, 500000000, time.UTC)
	link := "old-token"
	want := &PlayPurchase{
		State:               "SUBSCRIPTION_STATE_ACTIVE",
		ProductID:           "etu",
		BasePlanID:          "monthly",
		ExpiryTime:          &exp,
		AutoRenewing:        true,
		Acknowledged:        false,
		Test:                true,
		FreeTrial:           true,
		ObfuscatedAccountID: "ada-lovelace-account",
		LinkedPurchaseToken: &link,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("purchase mismatch (-want +got):\n%s", diff)
	}
}

func TestPlayGetSubscriptionMinimal(t *testing.T) {
	p := newTestPlay(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"subscriptionState":"SUBSCRIPTION_STATE_PENDING","acknowledgementState":"ACKNOWLEDGEMENT_STATE_ACKNOWLEDGED"}`)
	})

	got, err := p.GetSubscription(context.Background(), "tok")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	want := &PlayPurchase{State: "SUBSCRIPTION_STATE_PENDING", Acknowledged: true}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("purchase mismatch (-want +got):\n%s", diff)
	}
}

func TestPlayGetSubscriptionBadExpiry(t *testing.T) {
	p := newTestPlay(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"lineItems":[{"productId":"etu","expiryTime":"yesterday"}]}`)
	})

	if _, err := p.GetSubscription(context.Background(), "tok"); err == nil {
		t.Fatal("GetSubscription() error = nil, want parse error")
	}
}

func TestPlayNotFound(t *testing.T) {
	calls := map[string]func(*Play) error{
		"get":         func(p *Play) error { _, err := p.GetSubscription(context.Background(), "tok"); return err },
		"acknowledge": func(p *Play) error { return p.Acknowledge(context.Background(), "etu", "tok") },
		"cancel":      func(p *Play) error { return p.Cancel(context.Background(), "tok") },
	}
	for name, call := range calls {
		for _, status := range []int{http.StatusNotFound, http.StatusGone} {
			t.Run(name+"/"+http.StatusText(status), func(t *testing.T) {
				p := newTestPlay(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":{"code":`+strconv.Itoa(status)+`,"message":"gone"}}`)
				})
				if err := call(p); !errors.Is(err, ErrPlayPurchaseNotFound) {
					t.Errorf("error = %v, want ErrPlayPurchaseNotFound", err)
				}
			})
		}
	}
}

func TestPlayGetSubscriptionServerError(t *testing.T) {
	p := newTestPlay(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	err := func() error { _, err := p.GetSubscription(context.Background(), "tok"); return err }()
	if err == nil || errors.Is(err, ErrPlayPurchaseNotFound) {
		t.Errorf("error = %v, want non-not-found error", err)
	}
}

func TestPlayAcknowledgeAndCancelRequests(t *testing.T) {
	type seen struct {
		method, path string
		body         map[string]any
	}
	tests := []struct {
		name     string
		call     func(*Play) error
		wantPath string
		wantBody map[string]any
	}{
		{
			name:     "acknowledge",
			call:     func(p *Play) error { return p.Acknowledge(context.Background(), "etu", "tok-ada") },
			wantPath: "/androidpublisher/v3/applications/com.timeclimbers.etu/purchases/subscriptions/etu/tokens/tok-ada:acknowledge",
			wantBody: map[string]any{},
		},
		{
			name:     "cancel",
			call:     func(p *Play) error { return p.Cancel(context.Background(), "tok-ada") },
			wantPath: "/androidpublisher/v3/applications/com.timeclimbers.etu/purchases/subscriptionsv2/tokens/tok-ada:cancel",
			wantBody: map[string]any{"cancellationContext": map[string]any{"cancellationType": "DEVELOPER_REQUESTED_STOP_PAYMENTS"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got seen
			p := newTestPlay(t, func(w http.ResponseWriter, r *http.Request) {
				got.method, got.path = r.Method, r.URL.Path
				b, _ := io.ReadAll(r.Body)
				if len(b) > 0 {
					_ = json.Unmarshal(b, &got.body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{}`)
			})
			if err := tt.call(p); err != nil {
				t.Fatalf("call: %v", err)
			}
			if got.method != http.MethodPost || got.path != tt.wantPath {
				t.Errorf("request = %s %s, want POST %s", got.method, got.path, tt.wantPath)
			}
			if got.body == nil {
				got.body = map[string]any{}
			}
			if diff := cmp.Diff(tt.wantBody, got.body); diff != "" {
				t.Errorf("body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPlayStateCancelable(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{"SUBSCRIPTION_STATE_ACTIVE", true},
		{"SUBSCRIPTION_STATE_IN_GRACE_PERIOD", true},
		{"SUBSCRIPTION_STATE_ON_HOLD", true},
		{"SUBSCRIPTION_STATE_PAUSED", true},
		{"SUBSCRIPTION_STATE_CANCELED", false},
		{"SUBSCRIPTION_STATE_EXPIRED", false},
		{"SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED", false},
		{"SUBSCRIPTION_STATE_PENDING", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, PlayStateCancelable(tt.state)); diff != "" {
				t.Errorf("PlayStateCancelable mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
