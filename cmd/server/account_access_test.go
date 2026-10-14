package main

import (
	"context"
	"testing"
	"time"

	"go.icco.me/etu-backend/internal/auth"
	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// TestAccountPreferencesWithoutSubscription checks the unpaid security-settings exception.
func TestAccountPreferencesWithoutSubscription(t *testing.T) {
	for _, billing := range []string{"free", "canceled", "past_due"} {
		for _, caller := range []string{"m2m", "apikey"} {
			for _, tc := range []struct {
				name     string
				req      *pb.UpdateUserSettingsRequest
				disabled bool
				want     codes.Code
			}{
				{"password", &pb.UpdateUserSettingsRequest{UserId: "alice", Password: proto.String("newpassword"), CurrentPassword: proto.String("oldpassword")}, false, codes.OK},
				{"AI opt out", &pb.UpdateUserSettingsRequest{UserId: "alice", AiEnabled: proto.Bool(false)}, false, codes.OK},
				{"disabled", &pb.UpdateUserSettingsRequest{UserId: "alice", AiEnabled: proto.Bool(false)}, true, codes.PermissionDenied},
				{"profile mutation", &pb.UpdateUserSettingsRequest{UserId: "alice", AiEnabled: proto.Bool(false), Name: proto.String("Ada Lovelace")}, false, codes.PermissionDenied},
				{"empty mutation", &pb.UpdateUserSettingsRequest{UserId: "alice"}, false, codes.PermissionDenied},
			} {
				t.Run(billing+"/"+caller+"/"+tc.name, func(t *testing.T) {
					ctx := auth.SetAuthContext(context.Background(), "alice", caller)
					accounts := &fakeAccounts{state: auth.AccountState{Status: billing, Disabled: tc.disabled}}
					err := enforcePaywall(ctx, "/etu.UserSettingsService/UpdateUserSettings", tc.req, accounts, auth.Paywall{Grace: time.Hour})
					if status.Code(err) != tc.want {
						t.Fatalf("got %v, want %v", err, tc.want)
					}
				})
			}
		}
	}
}
