package auth

import (
	"context"
	"crypto/subtle"
	"os"
	"strings"

	"github.com/icco/gutil/logging"
)

// M2MConfig holds configuration for M2M token authentication
type M2MConfig struct {
	tokens []string
}

// NewM2MConfig creates a new M2M configuration from environment variables.
// It reads from GRPC_API_KEYS which should contain a comma-separated list of valid tokens.
// The provided context is used to log the configured/disabled status; loggers are
// resolved via gutil/logging.FromContext throughout the package.
func NewM2MConfig(ctx context.Context) *M2MConfig {
	l := logging.FromContext(ctx)
	config := &M2MConfig{}

	grpcApiKeys := os.Getenv("GRPC_API_KEYS")
	if grpcApiKeys != "" {
		rawTokens := strings.Split(grpcApiKeys, ",")
		for _, token := range rawTokens {
			trimmed := strings.TrimSpace(token)
			if trimmed != "" {
				config.tokens = append(config.tokens, trimmed)
			}
		}
		l.Infow("M2M authentication enabled", "token_count", len(config.tokens))
		return config
	}

	l.Infow("M2M authentication disabled - no GRPC_API_KEYS configured")
	return config
}

// IsEnabled returns true if M2M authentication is configured.
func (c *M2MConfig) IsEnabled() bool {
	return len(c.tokens) > 0
}

// ValidateToken checks if the provided token matches any configured M2M token.
// Returns true and the token index if valid, false and -1 otherwise.
// Uses constant-time comparison to prevent timing attacks.
func (c *M2MConfig) ValidateToken(token string) (bool, int) {
	for i, validToken := range c.tokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(validToken)) == 1 {
			return true, i
		}
	}
	return false, -1
}

// LogAuthentication logs successful M2M authentication with token index for audit purposes.
func (c *M2MConfig) LogAuthentication(ctx context.Context, method string, tokenIndex int) {
	logging.FromContext(ctx).Infow("authenticated request",
		"method", method,
		"auth_type", "m2m",
		"key_index", tokenIndex,
	)
}
