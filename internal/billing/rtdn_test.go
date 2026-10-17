package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/api/idtoken"
)

const (
	rtdnAudience       = "https://be.example.com/billing/play/rtdn"
	rtdnServiceAccount = "pubsub-push@example-project.iam.gserviceaccount.com"
	rtdnPackage        = "com.example.app"
	rtdnToken          = "token-ada"
)

type syncCall struct {
	token  string
	voided bool
}

type fakeSync struct {
	calls []syncCall
	err   error
}

func (f *fakeSync) sync(_ context.Context, token string, voided bool) error {
	f.calls = append(f.calls, syncCall{token: token, voided: voided})
	return f.err
}

func fakeValidator(email any, verified any) TokenValidator {
	return func(_ context.Context, token, audience string) (*idtoken.Payload, error) {
		if token != "good-jwt" {
			return nil, errors.New("bad signature")
		}
		claims := map[string]any{}
		if email != nil {
			claims["email"] = email
		}
		if verified != nil {
			claims["email_verified"] = verified
		}
		return &idtoken.Payload{Audience: audience, Claims: claims}, nil
	}
}

func okValidator() TokenValidator {
	return fakeValidator(rtdnServiceAccount, true)
}

func pushBody(t *testing.T, notification map[string]any) string {
	t.Helper()
	data, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	env, err := json.Marshal(map[string]any{"message": map[string]any{
		"data":      base64.StdEncoding.EncodeToString(data),
		"messageId": "1",
	}})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return string(env)
}

func subscriptionBody(t *testing.T, pkg string) string {
	return pushBody(t, map[string]any{
		"version":                  "1.0",
		"packageName":              pkg,
		"subscriptionNotification": map[string]any{"notificationType": 4, "purchaseToken": rtdnToken, "subscriptionId": "etu"},
	})
}

func doRTDN(t *testing.T, h http.Handler, method, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, "/billing/play/rtdn", strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newRTDN(f *fakeSync, v TokenValidator) http.Handler {
	var sync func(context.Context, string, bool) error
	if f != nil {
		sync = f.sync
	}
	return NewRTDNHandler(zap.NewNop().Sugar(), sync, v, rtdnAudience, rtdnServiceAccount, rtdnPackage)
}

func TestRTDNRejectsBadAudience(t *testing.T) {
	f := &fakeSync{}
	validate := func(_ context.Context, _, audience string) (*idtoken.Payload, error) {
		if audience != rtdnAudience {
			t.Errorf("audience = %q, want %q", audience, rtdnAudience)
		}
		return nil, errors.New("audience mismatch")
	}
	rec := doRTDN(t, newRTDN(f, validate), http.MethodPost, "Bearer good-jwt", subscriptionBody(t, rtdnPackage))
	if diff := cmp.Diff(http.StatusUnauthorized, rec.Code); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if len(f.calls) != 0 {
		t.Errorf("sync calls = %v, want none", f.calls)
	}
}

func TestRTDNRejectsUnauthenticated(t *testing.T) {
	tests := []struct {
		name string
		auth string
	}{
		{name: "missing header", auth: ""},
		{name: "not bearer", auth: "Basic good-jwt"},
		{name: "empty bearer", auth: "Bearer "},
		{name: "invalid jwt", auth: "Bearer forged-jwt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeSync{}
			rec := doRTDN(t, newRTDN(f, okValidator()), http.MethodPost, tt.auth, subscriptionBody(t, rtdnPackage))
			if diff := cmp.Diff(http.StatusUnauthorized, rec.Code); diff != "" {
				t.Errorf("status mismatch (-want +got):\n%s", diff)
			}
			if len(f.calls) != 0 {
				t.Errorf("sync calls = %v, want none", f.calls)
			}
		})
	}
}

func TestRTDNRejectsWrongEmail(t *testing.T) {
	tests := []struct {
		name     string
		validate TokenValidator
	}{
		{name: "other service account", validate: fakeValidator("mallory@example.com", true)},
		{name: "unverified email", validate: fakeValidator(rtdnServiceAccount, false)},
		{name: "email_verified missing", validate: fakeValidator(rtdnServiceAccount, nil)},
		{name: "email_verified not a bool", validate: fakeValidator(rtdnServiceAccount, "true")},
		{name: "email missing", validate: fakeValidator(nil, true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeSync{}
			rec := doRTDN(t, newRTDN(f, tt.validate), http.MethodPost, "Bearer good-jwt", subscriptionBody(t, rtdnPackage))
			if diff := cmp.Diff(http.StatusUnauthorized, rec.Code); diff != "" {
				t.Errorf("status mismatch (-want +got):\n%s", diff)
			}
			if len(f.calls) != 0 {
				t.Errorf("sync calls = %v, want none", f.calls)
			}
		})
	}
}

func TestRTDNOnlyAcceptsPost(t *testing.T) {
	f := &fakeSync{}
	rec := doRTDN(t, newRTDN(f, okValidator()), http.MethodGet, "Bearer good-jwt", "")
	if diff := cmp.Diff(http.StatusMethodNotAllowed, rec.Code); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
}

func TestRTDNSyncsSubscriptionToken(t *testing.T) {
	f := &fakeSync{}
	rec := doRTDN(t, newRTDN(f, okValidator()), http.MethodPost, "Bearer good-jwt", subscriptionBody(t, rtdnPackage))
	if diff := cmp.Diff(http.StatusNoContent, rec.Code); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]syncCall{{token: rtdnToken}}, f.calls, cmp.AllowUnexported(syncCall{})); diff != "" {
		t.Errorf("sync calls mismatch (-want +got):\n%s", diff)
	}
}

func TestRTDNVoidedPurchase(t *testing.T) {
	f := &fakeSync{}
	body := pushBody(t, map[string]any{
		"packageName":                rtdnPackage,
		"voidedPurchaseNotification": map[string]any{"purchaseToken": rtdnToken, "orderId": "GPA.1", "productType": 1, "refundType": 1},
	})
	rec := doRTDN(t, newRTDN(f, okValidator()), http.MethodPost, "Bearer good-jwt", body)
	if diff := cmp.Diff(http.StatusNoContent, rec.Code); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]syncCall{{token: rtdnToken, voided: true}}, f.calls, cmp.AllowUnexported(syncCall{})); diff != "" {
		t.Errorf("sync calls mismatch (-want +got):\n%s", diff)
	}
}

func TestRTDNTestNotification(t *testing.T) {
	f := &fakeSync{}
	body := pushBody(t, map[string]any{"packageName": rtdnPackage, "testNotification": map[string]any{"version": "1.0"}})
	rec := doRTDN(t, newRTDN(f, okValidator()), http.MethodPost, "Bearer good-jwt", body)
	if diff := cmp.Diff(http.StatusNoContent, rec.Code); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if len(f.calls) != 0 {
		t.Errorf("sync calls = %v, want none", f.calls)
	}
}

func TestRTDNIgnoresOtherNotifications(t *testing.T) {
	f := &fakeSync{}
	body := pushBody(t, map[string]any{"packageName": rtdnPackage, "oneTimeProductNotification": map[string]any{"purchaseToken": rtdnToken}})
	rec := doRTDN(t, newRTDN(f, okValidator()), http.MethodPost, "Bearer good-jwt", body)
	if diff := cmp.Diff(http.StatusNoContent, rec.Code); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
	if len(f.calls) != 0 {
		t.Errorf("sync calls = %v, want none", f.calls)
	}
}

func TestRTDNSyncErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "unknown user acks", err: fmt.Errorf("%w: %w", ErrRTDNPermanent, errors.New("no matching user")), want: http.StatusNoContent},
		{name: "purchase not found acks", err: fmt.Errorf("%w: %w", ErrRTDNPermanent, ErrPlayPurchaseNotFound), want: http.StatusNoContent},
		{name: "transient error retries", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeSync{err: tt.err}
			rec := doRTDN(t, newRTDN(f, okValidator()), http.MethodPost, "Bearer good-jwt", subscriptionBody(t, rtdnPackage))
			if diff := cmp.Diff(tt.want, rec.Code); diff != "" {
				t.Errorf("status mismatch (-want +got):\n%s", diff)
			}
			if len(f.calls) != 1 {
				t.Errorf("sync calls = %v, want one", f.calls)
			}
		})
	}
}

func TestRTDNNotConfigured(t *testing.T) {
	validate := func(context.Context, string, string) (*idtoken.Payload, error) {
		t.Error("validator called when play is not configured")
		return nil, errors.New("unexpected")
	}
	rec := doRTDN(t, newRTDN(nil, validate), http.MethodPost, "Bearer good-jwt", subscriptionBody(t, rtdnPackage))
	if diff := cmp.Diff(http.StatusServiceUnavailable, rec.Code); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
}

func TestRTDNDropsUnusableNotifications(t *testing.T) {
	tests := []struct {
		name    string
		body    func(*testing.T) string
		wantLog string
	}{
		{
			name:    "wrong package",
			body:    func(t *testing.T) string { return subscriptionBody(t, "com.example.other") },
			wantLog: "rtdn for another package dropped",
		},
		{
			name: "subscription without token",
			body: func(t *testing.T) string {
				return pushBody(t, map[string]any{"packageName": rtdnPackage, "subscriptionNotification": map[string]any{"notificationType": 4}})
			},
			wantLog: "rtdn notification without purchase token dropped",
		},
		{
			name: "voided purchase without token",
			body: func(t *testing.T) string {
				return pushBody(t, map[string]any{"packageName": rtdnPackage, "voidedPurchaseNotification": map[string]any{"orderId": "GPA.1"}})
			},
			wantLog: "rtdn notification without purchase token dropped",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeSync{}
			core, logs := observer.New(zap.WarnLevel)
			h := NewRTDNHandler(zap.New(core).Sugar(), f.sync, okValidator(), rtdnAudience, rtdnServiceAccount, rtdnPackage)
			rec := doRTDN(t, h, http.MethodPost, "Bearer good-jwt", tt.body(t))
			if diff := cmp.Diff(http.StatusNoContent, rec.Code); diff != "" {
				t.Errorf("status mismatch (-want +got):\n%s", diff)
			}
			if len(f.calls) != 0 {
				t.Errorf("sync calls = %v, want none", f.calls)
			}
			if got := logs.FilterMessage(tt.wantLog).Len(); got != 1 {
				t.Errorf("log %q count = %d, want 1 (logs %v)", tt.wantLog, got, logs.All())
			}
		})
	}
}

func TestRTDNMalformedBody(t *testing.T) {
	notBase64, err := json.Marshal(map[string]any{"message": map[string]any{"data": "!!!"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	notJSON, err := json.Marshal(map[string]any{"message": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("nope"))}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	tests := []struct {
		name string
		body string
	}{
		{name: "not json", body: "nope"},
		{name: "data not base64", body: string(notBase64)},
		{name: "data not json", body: string(notJSON)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeSync{}
			rec := doRTDN(t, newRTDN(f, okValidator()), http.MethodPost, "Bearer good-jwt", tt.body)
			if diff := cmp.Diff(http.StatusBadRequest, rec.Code); diff != "" {
				t.Errorf("status mismatch (-want +got):\n%s", diff)
			}
			if len(f.calls) != 0 {
				t.Errorf("sync calls = %v, want none", f.calls)
			}
		})
	}
}
