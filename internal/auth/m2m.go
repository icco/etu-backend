package auth

import (
	"log/slog"
	"os"
	"strings"
)

// M2MConfig holds configuration for M2M token authentication
type M2MConfig struct {
	tokens            []string
	isDeprecatedSetup bool
	logger            *slog.Logger
}

// NewM2MConfig creates a new M2M configuration from environment variables
// It supports both GRPC_API_KEYS (plural, recommended) and GRPC_API_KEY (singular, deprecated)
func NewM2MConfig(logger *slog.Logger) *M2MConfig {
	if logger == nil {
		logger = slog.Default()
	}

	config := &M2MConfig{
		logger: logger,
	}

	// Check for new multi-token configuration first
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
		config.isDeprecatedSetup = false
		logger.Info("M2M authentication enabled", "token_count", len(config.tokens), "config_type", "multi-token")
		return config
	}

	// Fall back to legacy single token for backwards compatibility
	grpcApiKey := os.Getenv("GRPC_API_KEY")
	if grpcApiKey != "" {
		config.tokens = []string{strings.TrimSpace(grpcApiKey)}
		config.isDeprecatedSetup = true
		logger.Warn("M2M authentication enabled with deprecated single-token configuration. Please migrate to GRPC_API_KEYS (plural) for token rotation support",
			"config_type", "single-token-deprecated")
		return config
	}

	// No M2M auth configured
	logger.Info("M2M authentication disabled - no GRPC_API_KEY or GRPC_API_KEYS configured")
	return config
}

// IsEnabled returns true if M2M authentication is configured
func (c *M2MConfig) IsEnabled() bool {
	return len(c.tokens) > 0
}

// ValidateToken checks if the provided token matches any configured M2M token
// Returns true and the token index if valid, false and -1 otherwise
func (c *M2MConfig) ValidateToken(token string) (bool, int) {
	for i, validToken := range c.tokens {
		if token == validToken {
			return true, i
		}
	}
	return false, -1
}

// LogAuthentication logs successful M2M authentication with token index for audit purposes
func (c *M2MConfig) LogAuthentication(method string, tokenIndex int) {
	if c.isDeprecatedSetup {
		c.logger.Info("authenticated request", "method", method, "auth_type", "m2m", "token_type", "deprecated-single")
	} else {
		c.logger.Info("authenticated request", "method", method, "auth_type", "m2m", "key_index", tokenIndex)
	}
}
