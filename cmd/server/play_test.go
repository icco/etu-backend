package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.uber.org/zap"
)

func TestPlayConfigFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    *playConfig
		wantErr string
	}{
		{name: "unset disables play", env: map[string]string{}},
		{
			name: "fully configured",
			env: map[string]string{
				"PLAY_PACKAGE_NAME": "com.example.app", "PLAY_RTDN_AUDIENCE": "https://be.example.com/billing/play/rtdn",
				"PLAY_RTDN_SERVICE_ACCOUNT": "push@example.iam.gserviceaccount.com",
			},
			want: &playConfig{packageName: "com.example.app", audience: "https://be.example.com/billing/play/rtdn", serviceAccount: "push@example.iam.gserviceaccount.com"},
		},
		{
			name: "test purchases allowed",
			env: map[string]string{
				"PLAY_PACKAGE_NAME": "com.example.app", "PLAY_RTDN_AUDIENCE": "aud", "PLAY_RTDN_SERVICE_ACCOUNT": "sa", "PLAY_ALLOW_TEST_PURCHASES": "true",
			},
			want: &playConfig{packageName: "com.example.app", audience: "aud", serviceAccount: "sa", allowTest: true},
		},
		{
			name:    "missing audience",
			env:     map[string]string{"PLAY_PACKAGE_NAME": "com.example.app", "PLAY_RTDN_SERVICE_ACCOUNT": "sa"},
			wantErr: "PLAY_RTDN_AUDIENCE",
		},
		{
			name:    "missing service account",
			env:     map[string]string{"PLAY_PACKAGE_NAME": "com.example.app", "PLAY_RTDN_AUDIENCE": "aud"},
			wantErr: "PLAY_RTDN_SERVICE_ACCOUNT",
		},
		{
			name: "invalid test purchases flag",
			env: map[string]string{
				"PLAY_PACKAGE_NAME": "com.example.app", "PLAY_RTDN_AUDIENCE": "aud", "PLAY_RTDN_SERVICE_ACCOUNT": "sa", "PLAY_ALLOW_TEST_PURCHASES": "maybe",
			},
			wantErr: "PLAY_ALLOW_TEST_PURCHASES",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"PLAY_PACKAGE_NAME", "PLAY_RTDN_AUDIENCE", "PLAY_RTDN_SERVICE_ACCOUNT", "PLAY_ALLOW_TEST_PURCHASES"} {
				t.Setenv(k, tt.env[k])
			}
			got, err := playConfigFromEnv()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("playConfigFromEnv: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(playConfig{}), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("config mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHealthHandlerMountsRTDN(t *testing.T) {
	rtdn := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := newHealthHandler(zap.NewNop().Sugar(), http.NotFoundHandler(), rtdn)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/billing/play/rtdn", nil))
	if diff := cmp.Diff(http.StatusNoContent, w.Code); diff != "" {
		t.Errorf("status mismatch (-want +got):\n%s", diff)
	}
}
