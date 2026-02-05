package auth

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewM2MConfig_MultiToken(t *testing.T) {
	// Test multi-token configuration
	t.Setenv("GRPC_API_KEYS", "token1,token2,token3")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	config := NewM2MConfig(logger)

	if !config.IsEnabled() {
		t.Error("Expected M2M auth to be enabled")
	}

	if len(config.tokens) != 3 {
		t.Errorf("Expected 3 tokens, got %d", len(config.tokens))
	}

	if config.isDeprecatedSetup {
		t.Error("Expected non-deprecated setup")
	}

	// Validate each token
	expectedTokens := []string{"token1", "token2", "token3"}
	for i, expected := range expectedTokens {
		if config.tokens[i] != expected {
			t.Errorf("Expected token[%d] to be %s, got %s", i, expected, config.tokens[i])
		}
	}
}

func TestNewM2MConfig_MultiTokenWithWhitespace(t *testing.T) {
	// Test with whitespace around tokens
	t.Setenv("GRPC_API_KEYS", " token1 , token2 ,  token3  ")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	config := NewM2MConfig(logger)

	if len(config.tokens) != 3 {
		t.Errorf("Expected 3 tokens, got %d", len(config.tokens))
	}

	expectedTokens := []string{"token1", "token2", "token3"}
	for i, expected := range expectedTokens {
		if config.tokens[i] != expected {
			t.Errorf("Expected token[%d] to be %s, got %s", i, expected, config.tokens[i])
		}
	}
}

func TestNewM2MConfig_SingleTokenDeprecated(t *testing.T) {
	// Test single token (deprecated) configuration
	t.Setenv("GRPC_API_KEY", "single-token")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	config := NewM2MConfig(logger)

	if !config.IsEnabled() {
		t.Error("Expected M2M auth to be enabled")
	}

	if len(config.tokens) != 1 {
		t.Errorf("Expected 1 token, got %d", len(config.tokens))
	}

	if !config.isDeprecatedSetup {
		t.Error("Expected deprecated setup flag to be true")
	}

	if config.tokens[0] != "single-token" {
		t.Errorf("Expected token to be 'single-token', got %s", config.tokens[0])
	}

	// Check that deprecation warning was logged
	logOutput := buf.String()
	if !strings.Contains(logOutput, "deprecated") {
		t.Error("Expected deprecation warning in logs")
	}
}

func TestNewM2MConfig_PrioritizesMultiToken(t *testing.T) {
	// If both are set, GRPC_API_KEYS should take precedence
	t.Setenv("GRPC_API_KEYS", "multi1,multi2")
	t.Setenv("GRPC_API_KEY", "single-token")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	config := NewM2MConfig(logger)

	if len(config.tokens) != 2 {
		t.Errorf("Expected 2 tokens from GRPC_API_KEYS, got %d", len(config.tokens))
	}

	if config.isDeprecatedSetup {
		t.Error("Expected non-deprecated setup when GRPC_API_KEYS is set")
	}
}

func TestNewM2MConfig_NoAuth(t *testing.T) {
	// Test with no auth configured - t.Setenv ensures clean environment

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	config := NewM2MConfig(logger)

	if config.IsEnabled() {
		t.Error("Expected M2M auth to be disabled")
	}

	if len(config.tokens) != 0 {
		t.Errorf("Expected 0 tokens, got %d", len(config.tokens))
	}
}

func TestValidateToken_ValidToken(t *testing.T) {
	t.Setenv("GRPC_API_KEYS", "token1,token2,token3")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	config := NewM2MConfig(logger)

	// Test each valid token
	tests := []struct {
		token         string
		expectedValid bool
		expectedIndex int
	}{
		{"token1", true, 0},
		{"token2", true, 1},
		{"token3", true, 2},
		{"invalid", false, -1},
		{"", false, -1},
	}

	for _, tt := range tests {
		valid, index := config.ValidateToken(tt.token)
		if valid != tt.expectedValid {
			t.Errorf("ValidateToken(%q): expected valid=%v, got %v", tt.token, tt.expectedValid, valid)
		}
		if index != tt.expectedIndex {
			t.Errorf("ValidateToken(%q): expected index=%d, got %d", tt.token, tt.expectedIndex, index)
		}
	}
}

func TestValidateToken_SingleToken(t *testing.T) {
	t.Setenv("GRPC_API_KEY", "my-secret-token")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	config := NewM2MConfig(logger)

	// Valid token should return true with index 0
	valid, index := config.ValidateToken("my-secret-token")
	if !valid {
		t.Error("Expected token to be valid")
	}
	if index != 0 {
		t.Errorf("Expected index 0, got %d", index)
	}

	// Invalid token should return false with index -1
	valid, index = config.ValidateToken("wrong-token")
	if valid {
		t.Error("Expected token to be invalid")
	}
	if index != -1 {
		t.Errorf("Expected index -1, got %d", index)
	}
}

func TestLogAuthentication(t *testing.T) {
	t.Run("MultiToken", func(t *testing.T) {
		t.Setenv("GRPC_API_KEYS", "token1,token2")

		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		config := NewM2MConfig(logger)
		buf.Reset() // Clear initialization logs

		config.LogAuthentication("/test.Service/Method", 1)

		logOutput := buf.String()
		if !strings.Contains(logOutput, "key_index=1") {
			t.Error("Expected key_index=1 in log output")
		}
		if !strings.Contains(logOutput, "auth_type=m2m") {
			t.Error("Expected auth_type=m2m in log output")
		}
	})

	t.Run("SingleTokenDeprecated", func(t *testing.T) {
		t.Setenv("GRPC_API_KEY", "token")

		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		config := NewM2MConfig(logger)
		buf.Reset() // Clear initialization logs

		config.LogAuthentication("/test.Service/Method", 0)

		logOutput := buf.String()
		if !strings.Contains(logOutput, "deprecated-single") {
			t.Error("Expected 'deprecated-single' in log output")
		}
		if !strings.Contains(logOutput, "auth_type=m2m") {
			t.Error("Expected auth_type=m2m in log output")
		}
	})
}

func TestNewM2MConfig_EmptyTokensIgnored(t *testing.T) {
	// Test with empty tokens in the list
	t.Setenv("GRPC_API_KEYS", "token1,,token2,  ,token3")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	config := NewM2MConfig(logger)

	// Should only get 3 valid tokens (empty ones ignored)
	if len(config.tokens) != 3 {
		t.Errorf("Expected 3 tokens (empty ones ignored), got %d", len(config.tokens))
	}

	expectedTokens := []string{"token1", "token2", "token3"}
	for i, expected := range expectedTokens {
		if config.tokens[i] != expected {
			t.Errorf("Expected token[%d] to be %s, got %s", i, expected, config.tokens[i])
		}
	}
}
