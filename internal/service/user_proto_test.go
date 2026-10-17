package service

import (
	"testing"

	"go.icco.me/etu-backend/internal/db"
	"google.golang.org/protobuf/proto"
)

func TestUserToProtoBillingProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider *string
	}{
		{name: "set", provider: proto.String("play")},
		{name: "unset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := userToProto(&db.User{ID: "ada", BillingProvider: tt.provider}).BillingProvider
			if tt.provider == nil {
				if got != nil {
					t.Fatalf("BillingProvider = %q, want nil", *got)
				}
				return
			}
			if got == nil || *got != *tt.provider {
				t.Fatalf("BillingProvider = %v, want %q", got, *tt.provider)
			}
		})
	}
}
