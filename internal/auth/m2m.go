package auth

import (
	"crypto/subtle"
	"log/slog"
	"os"
	"strings"
)

// M2MConfig holds configuration for M2M token authentication
type M2MConfig struct {
	tokens []string
	logger *slog.Logger
}

// NewM2MConfig creates a new M2M configuration from environment variables
// It reads from GRPC_API_KEYS which should contain a comma-separated list of valid tokens
func NewM2MConfig(logger *slog.Logger) *M2MConfig {
	if logger == nil {
		logger = slog.Default()
	}

	config := &M2MConfig{
		logger: logger,
	}

	// Read multi-token configuration
	grpcApiKeys := os.Getenv("GRPC_API_KEYS")
	if grpcApiKeys != "" {
		// Split by comma and trim whitespace
		rawTokens := strings.Split(grpcApiKeys, ",")
		for _, token := range rawTokens {
			trimmed := strings.TrimSpace(token)
			if trimmed != "" {
				config.tokens = append(config.tokens, trimmed)
			}
		}
		logger.Info("M2M authentication enabled", "token_count", len(config.tokens))
		return config
	}

	// No M2M auth configured
	logger.Info("M2M authentication disabled - no GRPC_API_KEYS configured")
	return config
}

// IsEnabled returns true if M2M authentication is configured
func (c *M2MConfig) IsEnabled() bool {
	return len(c.tokens) > 0
}

// ValidateToken checks if the provided token matches any configured M2M token
// Returns true and the token index if valid, false and -1 otherwise
// Uses constant-time comparison to prevent timing attacks
func (c *M2MConfig) ValidateToken(token string) (bool, int) {
	for i, validToken := range c.tokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(validToken)) == 1 {
			return true, i
		}
	}
	return false, -1
}

// LogAuthentication logs successful M2M authentication with token index for audit purposes
func (c *M2MConfig) LogAuthentication(method string, tokenIndex int) {
	c.logger.Info("authenticated request", "method", method, "auth_type", "m2m", "key_index", tokenIndex)
}
