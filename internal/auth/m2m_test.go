package auth

import (
	"context"
	"testing"
)

const (
	testToken1 = "token1"
	testToken2 = "token2"
	testToken3 = "token3"
)

func TestNewM2MConfig_MultiToken(t *testing.T) {
	t.Setenv("GRPC_API_KEYS", "token1,token2,token3")

	config := NewM2MConfig(context.Background())

	if !config.IsEnabled() {
		t.Error("Expected M2M auth to be enabled")
	}

	if len(config.tokens) != 3 {
		t.Errorf("Expected 3 tokens, got %d", len(config.tokens))
	}

	expectedTokens := []string{testToken1, testToken2, testToken3}
	for i, expected := range expectedTokens {
		if config.tokens[i] != expected {
			t.Errorf("Expected token[%d] to be %s, got %s", i, expected, config.tokens[i])
		}
	}
}

func TestNewM2MConfig_MultiTokenWithWhitespace(t *testing.T) {
	t.Setenv("GRPC_API_KEYS", " token1 , token2 ,  token3  ")

	config := NewM2MConfig(context.Background())

	if len(config.tokens) != 3 {
		t.Errorf("Expected 3 tokens, got %d", len(config.tokens))
	}

	expectedTokens := []string{testToken1, testToken2, testToken3}
	for i, expected := range expectedTokens {
		if config.tokens[i] != expected {
			t.Errorf("Expected token[%d] to be %s, got %s", i, expected, config.tokens[i])
		}
	}
}

func TestNewM2MConfig_NoAuth(t *testing.T) {
	config := NewM2MConfig(context.Background())

	if config.IsEnabled() {
		t.Error("Expected M2M auth to be disabled")
	}

	if len(config.tokens) != 0 {
		t.Errorf("Expected 0 tokens, got %d", len(config.tokens))
	}
}

func TestValidateToken_ValidToken(t *testing.T) {
	t.Setenv("GRPC_API_KEYS", "token1,token2,token3")

	config := NewM2MConfig(context.Background())

	tests := []struct {
		token         string
		expectedValid bool
		expectedIndex int
	}{
		{testToken1, true, 0},
		{testToken2, true, 1},
		{testToken3, true, 2},
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
	config := NewM2MConfig(context.Background())

	if config.IsEnabled() {
		t.Error("Expected M2M auth to be disabled")
	}

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

func TestLogAuthentication_doesNotPanic(t *testing.T) {
	t.Setenv("GRPC_API_KEYS", "token1,token2")

	config := NewM2MConfig(context.Background())

	// Smoke test: ensure LogAuthentication is callable. The exact log output is
	// produced by zap and not asserted here — we only care that it doesn't
	// panic and writes through the context-resolved logger.
	config.LogAuthentication(context.Background(), "/test.Service/Method", 1)
}

func TestNewM2MConfig_EmptyTokensIgnored(t *testing.T) {
	t.Setenv("GRPC_API_KEYS", "token1,,token2,  ,token3")

	config := NewM2MConfig(context.Background())

	if len(config.tokens) != 3 {
		t.Errorf("Expected 3 tokens (empty ones ignored), got %d", len(config.tokens))
	}

	expectedTokens := []string{testToken1, testToken2, testToken3}
	for i, expected := range expectedTokens {
		if config.tokens[i] != expected {
			t.Errorf("Expected token[%d] to be %s, got %s", i, expected, config.tokens[i])
		}
	}
}
