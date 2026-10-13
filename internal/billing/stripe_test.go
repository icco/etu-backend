package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stripe/stripe-go/v87"
)

func TestCancelSubscriptions(t *testing.T) {
	tests := []struct {
		name           string
		customerID     string
		subscriptionID string
		routes         map[string]func(w http.ResponseWriter)
		wantCalls      []string
		wantErr        bool
	}{
		{
			name:           "cancels active customer subscriptions and stored subscription",
			customerID:     "cus_1",
			subscriptionID: "sub_2",
			routes: map[string]func(w http.ResponseWriter){
				"GET /v1/subscriptions": func(w http.ResponseWriter) {
					_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"sub_1","status":"active"}],"has_more":false}`))
				},
				"DELETE /v1/subscriptions/sub_1": canceled("sub_1"),
				"GET /v1/subscriptions/sub_2": func(w http.ResponseWriter) {
					_, _ = w.Write([]byte(`{"id":"sub_2","status":"past_due"}`))
				},
				"DELETE /v1/subscriptions/sub_2": canceled("sub_2"),
			},
			wantCalls: []string{"GET /v1/subscriptions", "DELETE /v1/subscriptions/sub_1", "GET /v1/subscriptions/sub_2", "DELETE /v1/subscriptions/sub_2"},
		},
		{
			name:           "already canceled subscription is a no-op",
			subscriptionID: "sub_1",
			routes: map[string]func(w http.ResponseWriter){
				"GET /v1/subscriptions/sub_1": func(w http.ResponseWriter) {
					_, _ = w.Write([]byte(`{"id":"sub_1","status":"canceled"}`))
				},
			},
			wantCalls: []string{"GET /v1/subscriptions/sub_1"},
		},
		{
			name:           "missing subscription is a no-op",
			subscriptionID: "sub_gone",
			routes: map[string]func(w http.ResponseWriter){
				"GET /v1/subscriptions/sub_gone": stripeError(http.StatusNotFound, "resource_missing"),
			},
			wantCalls: []string{"GET /v1/subscriptions/sub_gone"},
		},
		{
			name:       "cancel failure is returned",
			customerID: "cus_1",
			routes: map[string]func(w http.ResponseWriter){
				"GET /v1/subscriptions": func(w http.ResponseWriter) {
					_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"sub_1","status":"active"}],"has_more":false}`))
				},
				"DELETE /v1/subscriptions/sub_1": stripeError(http.StatusBadRequest, "parameter_invalid_empty"),
			},
			wantCalls: []string{"GET /v1/subscriptions", "DELETE /v1/subscriptions/sub_1"},
			wantErr:   true,
		},
		{
			name: "no billing ids makes no calls",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			calls := []string{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key := r.Method + " " + r.URL.Path
				mu.Lock()
				calls = append(calls, key)
				mu.Unlock()
				if r.Method == http.MethodGet && r.URL.Path == "/v1/subscriptions" && r.URL.Query().Get("customer") != tt.customerID {
					t.Errorf("list customer = %q, want %q", r.URL.Query().Get("customer"), tt.customerID)
				}
				h, ok := tt.routes[key]
				if !ok {
					t.Errorf("unexpected request %s", key)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				h(w)
			}))
			defer srv.Close()

			b := NewStripe("sk_test_123", stripe.WithBackends(stripe.NewBackendsWithConfig(&stripe.BackendConfig{
				URL:               stripe.String(srv.URL),
				MaxNetworkRetries: stripe.Int64(0),
			})))
			err := b.CancelSubscriptions(context.Background(), tt.customerID, tt.subscriptionID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CancelSubscriptions err = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.wantCalls, calls, cmpEmpty); diff != "" {
				t.Errorf("calls mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

var cmpEmpty = cmp.Transformer("nilToEmpty", func(s []string) string { return strings.Join(s, "\n") })

func canceled(id string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"` + id + `","status":"canceled"}`))
	}
}

func stripeError(code int, errCode string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"` + errCode + `","message":"x"}}`))
	}
}
