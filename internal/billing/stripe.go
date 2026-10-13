package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/stripe/stripe-go/v87"
)

// Stripe cancels subscriptions through the Stripe API.
type Stripe struct {
	client *stripe.Client
}

// NewStripe returns a Stripe client authenticated with the given secret key.
func NewStripe(secretKey string, opts ...stripe.ClientOption) *Stripe {
	return &Stripe{client: stripe.NewClient(secretKey, opts...)}
}

// CancelSubscriptions immediately cancels every non-canceled subscription of
// the customer plus the given subscription. Already-canceled or missing
// subscriptions are treated as success so the call is safe to retry.
func (s *Stripe) CancelSubscriptions(ctx context.Context, customerID, subscriptionID string) error {
	done := map[string]bool{}
	if customerID != "" {
		params := &stripe.SubscriptionListParams{Customer: stripe.String(customerID)}
		for sub, err := range s.client.V1Subscriptions.List(ctx, params).All(ctx) {
			if err != nil {
				return fmt.Errorf("list stripe subscriptions: %w", err)
			}
			if err := s.cancel(ctx, sub.ID); err != nil {
				return err
			}
			done[sub.ID] = true
		}
	}
	if subscriptionID == "" || done[subscriptionID] {
		return nil
	}
	sub, err := s.client.V1Subscriptions.Retrieve(ctx, subscriptionID, nil)
	if isMissing(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("retrieve stripe subscription %s: %w", subscriptionID, err)
	}
	if sub.Status == stripe.SubscriptionStatusCanceled || sub.Status == stripe.SubscriptionStatusIncompleteExpired {
		return nil
	}
	return s.cancel(ctx, subscriptionID)
}

func (s *Stripe) cancel(ctx context.Context, id string) error {
	if _, err := s.client.V1Subscriptions.Cancel(ctx, id, nil); err != nil && !isMissing(err) {
		return fmt.Errorf("cancel stripe subscription %s: %w", id, err)
	}
	return nil
}

func isMissing(err error) bool {
	var se *stripe.Error
	return errors.As(err, &se) && se.HTTPStatusCode == http.StatusNotFound && se.Code == stripe.ErrorCodeResourceMissing
}
