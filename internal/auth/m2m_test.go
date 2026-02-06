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

func TestValidateToken_NoAuth(t *testing.T) {
	// Test ValidateToken when M2M auth is disabled (no tokens configured)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	config := NewM2MConfig(logger)

	// Verify M2M auth is disabled
	if config.IsEnabled() {
		t.Error("Expected M2M auth to be disabled")
	}

	// Test that ValidateToken returns false for any token when auth is disabled
	tests := []string{"any_token", "test", "", "valid-looking-token"}
	for _, token := range tests {
		valid, index := config.ValidateToken(token)
		if valid {
			t.Errorf("ValidateToken(%q): expected valid=false when M2M auth is disabled, got valid=true", token)
		}
		if index != -1 {
			t.Errorf("ValidateToken(%q): expected index=-1 when M2M auth is disabled, got %d", token, index)
		}
	}
}

func TestLogAuthentication(t *testing.T) {
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
