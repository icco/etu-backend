package main

import (
	"context"

	"go.icco.me/gutil/logging"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// runPendingAI consumes quota only after a successful fetch so broken objects
// cannot burn a user's daily cap without reaching the AI provider.
func runPendingAI[T any](
	ctx context.Context,
	items []T,
	limiter *rate.Limiter,
	owner func(T) string,
	fetch func(T) ([]byte, error),
	allow func(string) bool,
	call func(T, []byte) error,
) (int, int) {
	l := logging.FromContext(ctx)
	capped := map[string]bool{}
	processed, errs := 0, 0
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		userID := owner(it)
		if capped[userID] {
			continue
		}
		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				l.Errorw("rate limiter error", zap.Error(err))
				break
			}
		}
		data, err := fetch(it)
		if err != nil {
			errs++
			continue
		}
		if !allow(userID) {
			capped[userID] = true
			continue
		}
		if err := call(it, data); err != nil {
			errs++
			continue
		}
		processed++
	}
	return processed, errs
}
