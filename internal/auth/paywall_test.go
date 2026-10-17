package auth

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPaywallAuthorize(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-6 * 24 * time.Hour)
	old := now.Add(-8 * 24 * time.Hour)
	const notes = "/etu.NotesService/ListNotes"
	const getUser = "/etu.AuthService/GetUser"
	tests := []struct {
		name   string
		method string
		state  AccountState
		want   codes.Code
	}{
		{"active full access", notes, AccountState{Status: "active"}, codes.OK},
		{"trialing full access", notes, AccountState{Status: "trialing"}, codes.OK},
		{"etu-web trial full access", notes, AccountState{Status: "trial"}, codes.OK},
		{"legacy pro full access", notes, AccountState{Status: "pro"}, codes.OK},
		{"legacy premium full access", notes, AccountState{Status: "premium"}, codes.OK},
		{"inactive denied", notes, AccountState{Status: "inactive"}, codes.PermissionDenied},
		{"case mismatch denied", notes, AccountState{Status: "Active"}, codes.PermissionDenied},
		{"disabled pro", notes, AccountState{Status: "pro", Disabled: true}, codes.PermissionDenied},
		{"past_due within grace", notes, AccountState{Status: "past_due", PastDueSince: &recent}, codes.OK},
		{"past_due after grace", notes, AccountState{Status: "past_due", PastDueSince: &old}, codes.PermissionDenied},
		{"past_due without timestamp", notes, AccountState{Status: "past_due"}, codes.PermissionDenied},
		{"past_due after grace allowlisted", getUser, AccountState{Status: "past_due", PastDueSince: &old}, codes.OK},
		{"canceled denied", notes, AccountState{Status: "canceled"}, codes.PermissionDenied},
		{"unpaid denied", "/etu.StatsService/GetStats", AccountState{Status: "unpaid"}, codes.PermissionDenied},
		{"free denied", "/etu.TagsService/ListTags", AccountState{Status: "free"}, codes.PermissionDenied},
		{"empty denied", notes, AccountState{}, codes.PermissionDenied},
		{"canceled get user", getUser, AccountState{Status: "canceled"}, codes.OK},
		{"canceled settings", "/etu.UserSettingsService/GetUserSettings", AccountState{Status: "canceled"}, codes.OK},
		{"canceled list keys", "/etu.ApiKeysService/ListApiKeys", AccountState{Status: "canceled"}, codes.OK},
		{"canceled delete key", "/etu.ApiKeysService/DeleteApiKey", AccountState{Status: "canceled"}, codes.OK},
		{"canceled create key", "/etu.ApiKeysService/CreateApiKey", AccountState{Status: "canceled"}, codes.PermissionDenied},
		{"canceled update settings", "/etu.UserSettingsService/UpdateUserSettings", AccountState{Status: "canceled"}, codes.PermissionDenied},
		{"canceled subscription write", "/etu.AuthService/UpdateUserSubscription", AccountState{Status: "canceled"}, codes.PermissionDenied},
		{"unknown method denied", "/etu.Nope/Nope", AccountState{Status: "none"}, codes.PermissionDenied},
		{"disabled active", notes, AccountState{Status: "active", Disabled: true}, codes.PermissionDenied},
		{"canceled delete account", "/etu.AccountService/DeleteAccount", AccountState{Status: "canceled"}, codes.OK},
		{"canceled export data", "/etu.AccountService/ExportData", AccountState{Status: "canceled"}, codes.OK},
		{"disabled delete account", "/etu.AccountService/DeleteAccount", AccountState{Status: "canceled", Disabled: true}, codes.PermissionDenied},
		{"disabled export data", "/etu.AccountService/ExportData", AccountState{Status: "active", Disabled: true}, codes.PermissionDenied},
		{"disabled allowlisted", getUser, AccountState{Status: "active", Disabled: true}, codes.PermissionDenied},
	}
	p := Paywall{Grace: 7 * 24 * time.Hour}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := status.Code(p.Authorize(tt.method, tt.state, now))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Authorize() code mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNewPaywallFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		want    time.Duration
		wantErr bool
	}{
		{"default", "", 7 * 24 * time.Hour, false},
		{"hours", "48h", 48 * time.Hour, false},
		{"zero", "0s", 0, false},
		{"negative", "-1h", 0, true},
		{"invalid", "seven", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PAYWALL_GRACE_PERIOD", tt.env)
			got, err := NewPaywallFromEnv()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				if diff := cmp.Diff(tt.want, got.Grace); diff != "" {
					t.Errorf("Grace mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestGetAccountState(t *testing.T) {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		rows    *sqlmock.Rows
		want    AccountState
		wantErr bool
	}{
		{"found", sqlmock.NewRows([]string{"subscriptionStatus", "disabled", "pastDueSince"}).AddRow("past_due", true, since), AccountState{Status: "past_due", Disabled: true, PastDueSince: &since}, false},
		{"null since", sqlmock.NewRows([]string{"subscriptionStatus", "disabled", "pastDueSince"}).AddRow("active", false, nil), AccountState{Status: "active"}, false},
		{"missing", sqlmock.NewRows([]string{"subscriptionStatus", "disabled", "pastDueSince"}), AccountState{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock.New: %v", err)
			}
			defer func() { _ = db.Close() }()
			mock.ExpectQuery(`SELECT "subscriptionStatus", "disabled", "pastDueSince" FROM "User" WHERE id = \$1`).
				WithArgs("u1").WillReturnRows(tt.rows)
			got, err := (&Authenticator{db: db}).GetAccountState(context.Background(), "u1")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("state mismatch (-want +got):\n%s", diff)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unmet: %v", err)
			}
		})
	}
}

func TestPaywallExemptsVerifyPlayPurchase(t *testing.T) {
	p := Paywall{Grace: 7 * 24 * time.Hour}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		state AccountState
		want  codes.Code
	}{
		{name: "unpaid user may verify", state: AccountState{Status: "canceled"}, want: codes.OK},
		{name: "new user may verify", state: AccountState{}, want: codes.OK},
		{name: "disabled user may not verify", state: AccountState{Status: "canceled", Disabled: true}, want: codes.PermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := status.Code(p.Authorize("/etu.BillingService/VerifyPlayPurchase", tt.state, now))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Authorize() code mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
