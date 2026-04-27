// Package logger constructs the application's structured logger backed by
// icco/gutil/logging (a thin wrapper over zap). Loggers are propagated via
// context.Context throughout the codebase using logging.NewContext /
// logging.FromContext.
//
// Each binary entrypoint is expected to pass its own service name to New so
// that logs emitted by the different binaries built from this repo
// (cmd/server, cmd/sync, cmd/taggen) can be distinguished in log
// aggregators.
package logger

import (
	"github.com/icco/gutil/logging"
	"go.uber.org/zap"
)

// New returns the application logger tagged with the given service name.
// Most call sites should use logging.FromContext(ctx) instead of this
// directly; New is kept for the process entrypoint that needs to seed the
// root context.
func New(service string) *zap.SugaredLogger {
	return logging.Must(logging.NewLogger(service))
}
