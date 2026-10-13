package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestVerifyAPIKeyMalformed(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"prefix only", "etu_"},
		{"short", "etu_x"},
		{"eleven chars", "etu_1234567"},
		{"too short hex", "etu_" + strings.Repeat("a", 63)},
		{"too long hex", "etu_" + strings.Repeat("a", 65)},
		{"non hex", "etu_" + strings.Repeat("g", 64)},
		{"uppercase hex", "etu_" + strings.Repeat("A", 64)},
		{"wrong prefix", "abc_" + strings.Repeat("a", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			a := &Authenticator{db: conn}

			got, err := a.VerifyAPIKey(context.Background(), tt.key)
			if diff := cmp.Diff("", got); diff != "" {
				t.Errorf("userID mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(codes.Unauthenticated, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s", diff)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unexpected db calls: %v", err)
			}
		})
	}
}
