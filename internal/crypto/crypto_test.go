package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

// generateTestKey generates a random 32-byte key for testing
func generateTestKey() string {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestEncryptDecrypt(t *testing.T) {
	// Set up test encryption key
	testKey := generateTestKey()
	os.Setenv("ENCRYPTION_KEY", testKey)
	defer os.Unsetenv("ENCRYPTION_KEY")

	tests := []struct {
		name      string
		plaintext string
	}{
		{
			name:      "simple text",
			plaintext: "secret_ntn_1234567890abcdef",
		},
		{
			name:      "notion api key",
			plaintext: "secret_ntn_VQ3MNvj9tM8YvxDrRJKZO8yG7h8rQtP8Dq9WoGf3T8J",
		},
		{
			name:      "empty string",
			plaintext: "",
		},
		{
			name:      "unicode text",
			plaintext: "Hello 世界 🌍",
		},
		{
			name:      "multiline text",
			plaintext: "line1\nline2\nline3",
		},
		{
			name:      "long text",
			plaintext: "This is a very long text that should still be encrypted and decrypted correctly. " + string(make([]byte, 1000)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encrypt
			ciphertext, err := Encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("Encrypt() error = %v", err)
			}

			// Empty plaintext should return empty ciphertext
			if tt.plaintext == "" {
				if ciphertext != "" {
					t.Errorf("Encrypt('') should return empty string, got %q", ciphertext)
				}
				return
			}

			// Ciphertext should not equal plaintext
			if ciphertext == tt.plaintext {
				t.Errorf("Encrypt() ciphertext equals plaintext")
			}

			// Ciphertext should be base64 encoded
			if _, err := base64.StdEncoding.DecodeString(ciphertext); err != nil {
				t.Errorf("Encrypt() did not return valid base64: %v", err)
			}

			// Decrypt
			decrypted, err := Decrypt(ciphertext)
			if err != nil {
				t.Fatalf("Decrypt() error = %v", err)
			}

			// Decrypted should equal original plaintext
			if decrypted != tt.plaintext {
				t.Errorf("Decrypt() = %q, want %q", decrypted, tt.plaintext)
			}
		})
	}
}

func TestEncryptDeterministic(t *testing.T) {
	// Set up test encryption key
	testKey := generateTestKey()
	os.Setenv("ENCRYPTION_KEY", testKey)
	defer os.Unsetenv("ENCRYPTION_KEY")

	plaintext := "test_secret"

	// Encrypt twice
	ciphertext1, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	ciphertext2, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// Ciphertexts should be different due to random nonce
	if ciphertext1 == ciphertext2 {
		t.Errorf("Encrypt() should produce different ciphertexts for same plaintext")
	}

	// But both should decrypt to the same plaintext
	decrypted1, err := Decrypt(ciphertext1)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	decrypted2, err := Decrypt(ciphertext2)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	if decrypted1 != plaintext || decrypted2 != plaintext {
		t.Errorf("Decrypt() failed to recover original plaintext")
	}
}

func TestGetEncryptionKey(t *testing.T) {
	tests := []struct {
		name      string
		envValue  string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "valid 32-byte key",
			envValue:  generateTestKey(),
			wantError: false,
		},
		{
			name:      "missing key",
			envValue:  "",
			wantError: true,
			errorMsg:  "ENCRYPTION_KEY environment variable not set",
		},
		{
			name:      "invalid base64",
			envValue:  "not-valid-base64!!!",
			wantError: true,
			errorMsg:  "failed to decode ENCRYPTION_KEY",
		},
		{
			name:      "wrong size key (16 bytes)",
			envValue:  base64.StdEncoding.EncodeToString(make([]byte, 16)),
			wantError: true,
			errorMsg:  "ENCRYPTION_KEY must be 32 bytes",
		},
		{
			name:      "wrong size key (64 bytes)",
			envValue:  base64.StdEncoding.EncodeToString(make([]byte, 64)),
			wantError: true,
			errorMsg:  "ENCRYPTION_KEY must be 32 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable
			if tt.envValue != "" {
				os.Setenv("ENCRYPTION_KEY", tt.envValue)
				defer os.Unsetenv("ENCRYPTION_KEY")
			} else {
				os.Unsetenv("ENCRYPTION_KEY")
			}

			key, err := GetEncryptionKey()

			if tt.wantError {
				if err == nil {
					t.Errorf("GetEncryptionKey() expected error containing %q, got nil", tt.errorMsg)
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("GetEncryptionKey() error = %q, want error containing %q", err.Error(), tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("GetEncryptionKey() unexpected error = %v", err)
				}
				if len(key) != 32 {
					t.Errorf("GetEncryptionKey() returned key of length %d, want 32", len(key))
				}
			}
		})
	}
}

func TestDecryptInvalidData(t *testing.T) {
	// Set up test encryption key
	testKey := generateTestKey()
	os.Setenv("ENCRYPTION_KEY", testKey)
	defer os.Unsetenv("ENCRYPTION_KEY")

	tests := []struct {
		name       string
		ciphertext string
		wantError  bool
	}{
		{
			name:       "invalid base64",
			ciphertext: "not-valid-base64!!!",
			wantError:  true,
		},
		{
			name:       "too short ciphertext",
			ciphertext: base64.StdEncoding.EncodeToString([]byte("short")),
			wantError:  true,
		},
		{
			name:       "corrupted ciphertext",
			ciphertext: base64.StdEncoding.EncodeToString(make([]byte, 100)),
			wantError:  true,
		},
		{
			name:       "empty string",
			ciphertext: "",
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decrypt(tt.ciphertext)

			if tt.wantError && err == nil {
				t.Errorf("Decrypt() expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Decrypt() unexpected error = %v", err)
			}
		})
	}
}

func TestEncryptWithoutKey(t *testing.T) {
	// Ensure no encryption key is set
	os.Unsetenv("ENCRYPTION_KEY")

	_, err := Encrypt("test")
	if err == nil {
		t.Errorf("Encrypt() without ENCRYPTION_KEY should fail")
	}
}

func TestDecryptWithoutKey(t *testing.T) {
	// First encrypt with a key
	testKey := generateTestKey()
	os.Setenv("ENCRYPTION_KEY", testKey)
	ciphertext, err := Encrypt("test")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// Then try to decrypt without key
	os.Unsetenv("ENCRYPTION_KEY")

	_, err = Decrypt(ciphertext)
	if err == nil {
		t.Errorf("Decrypt() without ENCRYPTION_KEY should fail")
	}
}

func TestDecryptWithDifferentKey(t *testing.T) {
	// Encrypt with one key
	testKey1 := generateTestKey()
	os.Setenv("ENCRYPTION_KEY", testKey1)
	ciphertext, err := Encrypt("test")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// Try to decrypt with a different key
	testKey2 := generateTestKey()
	os.Setenv("ENCRYPTION_KEY", testKey2)
	defer os.Unsetenv("ENCRYPTION_KEY")

	_, err = Decrypt(ciphertext)
	if err == nil {
		t.Errorf("Decrypt() with different key should fail")
	}
}
