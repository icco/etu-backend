package service

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/db"
	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestBillingRPCsRequireM2M(t *testing.T) {
	calls := map[string]func(*AuthService, context.Context) error{
		"GetUserByStripeCustomerId": func(s *AuthService, ctx context.Context) error {
			_, err := s.GetUserByStripeCustomerId(ctx, &pb.GetUserByStripeCustomerIdRequest{StripeCustomerId: "cus_alice"})
			return err
		},
		"UpdateUserSubscription": func(s *AuthService, ctx context.Context) error {
			_, err := s.UpdateUserSubscription(ctx, &pb.UpdateUserSubscriptionRequest{UserId: "alice", SubscriptionStatus: "active", StripeEventId: proto.String("evt_alice")})
			return err
		},
		"UpdateUserStripeCustomer": func(s *AuthService, ctx context.Context) error {
			_, err := s.UpdateUserStripeCustomer(ctx, &pb.UpdateUserStripeCustomerRequest{UserId: "alice", StripeEventId: proto.String("evt_alice")})
			return err
		},
	}
	ctxs := map[string]context.Context{
		"own api key":     auth.SetAuthContext(context.Background(), "alice", "apikey"),
		"other api key":   auth.SetAuthContext(context.Background(), "bob", "apikey"),
		"unauthenticated": context.Background(),
	}
	for name, call := range calls {
		for ctxName, ctx := range ctxs {
			t.Run(name+"/"+ctxName, func(t *testing.T) {
				conn, mock, err := sqlmock.New()
				if err != nil {
					t.Fatalf("sqlmock.New: %v", err)
				}
				defer func() { _ = conn.Close() }()
				database, err := db.NewFromConn(conn)
				if err != nil {
					t.Fatalf("db.NewFromConn: %v", err)
				}
				err = call(&AuthService{db: database}, ctx)
				if diff := cmp.Diff(codes.PermissionDenied, status.Code(err)); diff != "" {
					t.Errorf("code mismatch (-want +got):\n%s (err=%v)", diff, err)
				}
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Errorf("unexpected db activity: %v", err)
				}
			})
		}
	}
}
