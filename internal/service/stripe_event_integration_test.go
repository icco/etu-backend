package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"

	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/db"
	"go.icco.me/etu-backend/internal/models"
	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type stripeUpdateCall func(*AuthService, context.Context, string, *string, string) (*pb.User, error)

func stripeUpdateCalls() map[string]stripeUpdateCall {
	return map[string]stripeUpdateCall{
		"subscription": func(s *AuthService, ctx context.Context, id string, event *string, value string) (*pb.User, error) {
			response, err := s.UpdateUserSubscription(ctx, &pb.UpdateUserSubscriptionRequest{UserId: id, StripeEventId: event, SubscriptionStatus: value})
			return response.GetUser(), err
		},
		"customer": func(s *AuthService, ctx context.Context, id string, event *string, value string) (*pb.User, error) {
			response, err := s.UpdateUserStripeCustomer(ctx, &pb.UpdateUserStripeCustomerRequest{UserId: id, StripeEventId: event, Name: &value})
			return response.GetUser(), err
		},
	}
}

func stripeTestService(t *testing.T) (*AuthService, *sql.DB) {
	t.Helper()
	url := os.Getenv("STRIPE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("STRIPE_TEST_DATABASE_URL is not set")
	}
	conn, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	database, err := db.NewFromConn(conn)
	if err != nil {
		t.Fatal(err)
	}
	return NewAuthService(database), conn
}

func stripeTestUser(ctx context.Context, t *testing.T, s *AuthService) string {
	t.Helper()
	user, err := s.db.CreateUser(ctx, fmt.Sprintf("alice-%s@example.com", models.GenerateCUID()), "hash")
	if err != nil {
		t.Fatal(err)
	}
	return user.ID
}

func stripeContext(t *testing.T) context.Context {
	return auth.SetAuthContext(t.Context(), "", "m2m")
}

func TestStripeEventDuplicatePostgres(t *testing.T) {
	for name, call := range stripeUpdateCalls() {
		t.Run(name, func(t *testing.T) {
			s, _ := stripeTestService(t)
			id := stripeTestUser(t.Context(), t, s)
			event := "evt_" + id
			ctx := stripeContext(t)
			if _, err := call(s, ctx, id, &event, "first"); err != nil {
				t.Fatal(err)
			}
			current, err := call(s, ctx, id, nil, "current")
			if err != nil {
				t.Fatal(err)
			}
			restarted, _ := stripeTestService(t)
			duplicate, err := call(restarted, ctx, id, &event, "stale")
			if err != nil || !proto.Equal(current, duplicate) {
				t.Fatalf("duplicate changed current user: got %v, want %v, err %v", duplicate, current, err)
			}
			for otherName, otherCall := range stripeUpdateCalls() {
				if otherName == name {
					continue
				}
				duplicate, err = otherCall(restarted, ctx, id, &event, "stale")
				if err != nil || !proto.Equal(current, duplicate) {
					t.Fatalf("event was not globally deduplicated: %v, %v", duplicate, err)
				}
			}
			empty := ""
			for _, value := range []string{"second", "third"} {
				updated, err := call(s, ctx, id, &empty, value)
				if err != nil || (updated.GetName() != value && updated.GetSubscriptionStatus() != value) {
					t.Fatalf("empty event did not update user: %v, %v", updated, err)
				}
			}
		})
	}
}

func TestStripeEventConcurrentPostgres(t *testing.T) {
	for name, call := range stripeUpdateCalls() {
		t.Run(name, func(t *testing.T) {
			first, _ := stripeTestService(t)
			second, _ := stripeTestService(t)
			id := stripeTestUser(t.Context(), t, first)
			event := "evt_" + id
			ctx := stripeContext(t)
			const deliveries = 16
			users := make([]*pb.User, deliveries)
			errs := make([]error, deliveries)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range deliveries {
				wg.Go(func() {
					<-start
					s := first
					if i%2 == 0 {
						s = second
					}
					users[i], errs[i] = call(s, ctx, id, &event, fmt.Sprintf("value-%d", i))
				})
			}
			close(start)
			wg.Wait()
			for i := range deliveries {
				if errs[i] != nil || users[i] == nil || !proto.Equal(users[0], users[i]) {
					t.Errorf("delivery %d did not return the single winning update: %v, %v", i, users[i], errs[i])
				}
			}
		})
	}
}

func TestStripeEventRollbackPostgres(t *testing.T) {
	for name, call := range stripeUpdateCalls() {
		t.Run(name, func(t *testing.T) {
			s, conn := stripeTestService(t)
			id := stripeTestUser(t.Context(), t, s)
			event := "evt_" + id
			ctx := stripeContext(t)
			if _, err := conn.ExecContext(ctx, `ALTER TABLE "User" ADD CONSTRAINT stripe_test_failure CHECK ("subscriptionStatus" != 'rejected' AND name IS DISTINCT FROM 'rejected')`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := conn.ExecContext(context.WithoutCancel(ctx), `ALTER TABLE "User" DROP CONSTRAINT stripe_test_failure`); err != nil {
					t.Error(err)
				}
			})
			if _, err := call(s, ctx, id, &event, "rejected"); err == nil {
				t.Fatal("expected transaction failure")
			}
			var count int
			if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM "StripeEvent" WHERE id = $1`, event).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed event was retained: count %d, error %v", count, err)
			}
			user, err := call(s, ctx, id, &event, "retry")
			if err != nil || (user.GetName() != "retry" && user.GetSubscriptionStatus() != "retry") {
				t.Fatalf("failed event could not retry: %v, %v", user, err)
			}
		})
	}
}

func TestStripeEventCommitFailurePostgres(t *testing.T) {
	s, conn := stripeTestService(t)
	ctx := stripeContext(t)
	if _, err := conn.ExecContext(ctx, `CREATE TABLE stripe_test_commits (id text PRIMARY KEY);
		ALTER TABLE "StripeEvent" ADD CONSTRAINT stripe_test_commit_failure FOREIGN KEY (id) REFERENCES stripe_test_commits(id) DEFERRABLE INITIALLY DEFERRED NOT VALID`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := conn.ExecContext(context.WithoutCancel(ctx), `ALTER TABLE "StripeEvent" DROP CONSTRAINT stripe_test_commit_failure; DROP TABLE stripe_test_commits`); err != nil {
			t.Error(err)
		}
	})
	for name, call := range stripeUpdateCalls() {
		t.Run(name, func(t *testing.T) {
			id := stripeTestUser(ctx, t, s)
			event := "evt_" + id
			before, err := s.db.GetUser(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if user, err := call(s, ctx, id, &event, "uncommitted"); err == nil || user != nil {
				t.Fatalf("commit failure returned success: %v, %v", user, err)
			}
			after, err := s.db.GetUser(ctx, id)
			if err != nil || !proto.Equal(userToProto(before), userToProto(after)) {
				t.Fatalf("failed commit persisted the update: %v, %v", after, err)
			}
			if _, err := conn.ExecContext(ctx, `INSERT INTO stripe_test_commits(id) VALUES ($1)`, event); err != nil {
				t.Fatal(err)
			}
			user, err := call(s, ctx, id, &event, "retry")
			if err != nil || (user.GetName() != "retry" && user.GetSubscriptionStatus() != "retry") {
				t.Fatalf("commit failure prevented retry: %v, %v", user, err)
			}
		})
	}
}

func TestStripeEventMissingUserPostgres(t *testing.T) {
	for name, call := range stripeUpdateCalls() {
		t.Run(name, func(t *testing.T) {
			s, _ := stripeTestService(t)
			event := "evt_" + models.GenerateCUID()
			ctx := stripeContext(t)
			if user, err := call(s, ctx, "missing", &event, "first"); err == nil || user != nil {
				t.Fatalf("missing user returned success: %v, %v", user, err)
			}
			id := stripeTestUser(ctx, t, s)
			user, err := call(s, ctx, id, &event, "retry")
			if err != nil || (user.GetName() != "retry" && user.GetSubscriptionStatus() != "retry") {
				t.Fatalf("missing user consumed event: %v, %v", user, err)
			}
		})
	}
}

func TestStripeEventCustomerLinkPostgres(t *testing.T) {
	s, conn := stripeTestService(t)
	ctx := stripeContext(t)
	id := stripeTestUser(ctx, t, s)
	customer := "cus_" + id
	if _, err := s.UpdateUserSubscription(ctx, &pb.UpdateUserSubscriptionRequest{UserId: id, SubscriptionStatus: "canceled", StripeCustomerId: proto.String("")}); err != nil {
		t.Fatal(err)
	}
	event := "evt_" + id
	_, err := s.UpdateUserSubscription(ctx, &pb.UpdateUserSubscriptionRequest{UserId: id, SubscriptionStatus: "active", StripeEventId: &event, ExpectedStripeCustomerId: &customer})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("cleared link did not fail precondition: %v", err)
	}
	user, err := s.db.GetUser(ctx, id)
	if err != nil || user.SubscriptionStatus != "canceled" {
		t.Fatalf("mismatch applied update: %v, %v", user, err)
	}
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM "StripeEvent" WHERE id = $1`, event).Scan(&count); err != nil || count != 0 {
		t.Fatalf("mismatched event was recorded: count %d, error %v", count, err)
	}
}

func TestStripeEventCustomerLinkConcurrentPostgres(t *testing.T) {
	first, conn := stripeTestService(t)
	second, _ := stripeTestService(t)
	ctx := stripeContext(t)
	id := stripeTestUser(ctx, t, first)
	customer := "cus_" + id
	if _, err := first.UpdateUserSubscription(ctx, &pb.UpdateUserSubscriptionRequest{UserId: id, SubscriptionStatus: "trialing", StripeCustomerId: &customer}); err != nil {
		t.Fatal(err)
	}
	const deliveries = 16
	errs := make([]error, deliveries)
	events := make([]string, deliveries)
	var deleteErr error
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range deliveries {
		events[i] = fmt.Sprintf("evt_%s_%d", id, i)
		wg.Go(func() {
			<-start
			s := first
			if i%2 == 0 {
				s = second
			}
			_, errs[i] = s.UpdateUserSubscription(ctx, &pb.UpdateUserSubscriptionRequest{UserId: id, SubscriptionStatus: "active", StripeEventId: &events[i], ExpectedStripeCustomerId: &customer})
		})
	}
	wg.Go(func() {
		<-start
		deleted := "evt_deleted_" + id
		_, deleteErr = second.UpdateUserSubscription(ctx, &pb.UpdateUserSubscriptionRequest{UserId: id, SubscriptionStatus: "canceled", StripeCustomerId: proto.String(""), StripeEventId: &deleted})
	})
	close(start)
	wg.Wait()
	if deleteErr != nil {
		t.Fatal(deleteErr)
	}
	user, err := first.db.GetUser(ctx, id)
	if err != nil || user.SubscriptionStatus != "canceled" || (user.StripeCustomerID != nil && *user.StripeCustomerID != "") {
		t.Fatalf("stale update survived customer deletion: %v, %v", user, err)
	}
	for i, err := range errs {
		var count int
		if qerr := conn.QueryRowContext(ctx, `SELECT count(*) FROM "StripeEvent" WHERE id = $1`, events[i]).Scan(&count); qerr != nil {
			t.Fatal(qerr)
		}
		switch status.Code(err) {
		case codes.OK:
			if count != 1 {
				t.Errorf("delivery %d applied without recording its event", i)
			}
		case codes.FailedPrecondition:
			if count != 0 {
				t.Errorf("delivery %d failed but recorded its event", i)
			}
		default:
			t.Errorf("delivery %d: unexpected error %v", i, err)
		}
	}
}
