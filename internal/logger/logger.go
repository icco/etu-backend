package logger

import (
	"log/slog"
	"os"
)

// New creates a new JSON structured logger for the application.
// It writes to stdout and uses JSON format for easy parsing by log aggregation systems.
func New() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	return slog.New(handler)
}
