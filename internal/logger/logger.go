// Package logger constructs the application's structured logger backed by
// icco/gutil/logging (a thin wrapper over zap). Loggers are propagated via
// context.Context throughout the codebase using logging.NewContext /
// logging.FromContext.
package logger

import (
	"github.com/icco/gutil/logging"
	"go.uber.org/zap"
)

const service = "etu-backend"

// New returns the application logger. Most call sites should use
// logging.FromContext(ctx) instead of this directly; New is kept for the
// process entrypoint that needs to seed the root context.
func New() *zap.SugaredLogger {
	return logging.Must(logging.NewLogger(service))
}
