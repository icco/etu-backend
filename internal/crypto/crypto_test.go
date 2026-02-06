package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"strings"
	"sync"
	"testing"
)

// resetEncryptionKeyCache resets the cached encryption key for testing
func resetEncryptionKeyCache() {
	encryptionKeyOnce = sync.Once{}
	encryptionKey = nil
	encryptionKeyErr = nil
}

// generateTestKey generates a random 32-byte key for testing
func generateTestKey() string {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// setTestEncryptionKey sets the encryption key directly for testing purposes
// This bypasses the GCP Secret Manager check
func setTestEncryptionKey(keyBase64 string) error {
	decoded, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return err
	}
	if len(decoded) != 32 {
		return nil
	}
	
	// Set the key and mark as initialized
	encryptionKeyOnce.Do(func() {
		encryptionKey = decoded
		encryptionKeyErr = nil
	})
	
	return nil
}

func TestEncryptDecrypt(t *testing.T) {
	// Set up test encryption key directly (bypassing GCP)
	testKey := generateTestKey()
	resetEncryptionKeyCache()
	if err := setTestEncryptionKey(testKey); err != nil {
		t.Fatalf("Failed to set test encryption key: %v", err)
	}

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
	// Set up test encryption key directly (bypassing GCP)
	testKey := generateTestKey()
	resetEncryptionKeyCache()
	if err := setTestEncryptionKey(testKey); err != nil {
		t.Fatalf("Failed to set test encryption key: %v", err)
	}

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
	t.Run("missing GCP_SECRET_NAME", func(t *testing.T) {
		// Reset cache for test
		resetEncryptionKeyCache()

		// Ensure GCP_SECRET_NAME is not set
		if err := os.Unsetenv("GCP_SECRET_NAME"); err != nil {
			t.Fatalf("Failed to unset GCP_SECRET_NAME: %v", err)
		}

		_, err := GetEncryptionKey()
		if err == nil {
			t.Errorf("GetEncryptionKey() expected error when GCP_SECRET_NAME not set, got nil")
		} else if !strings.Contains(err.Error(), "GCP_SECRET_NAME") {
			t.Errorf("GetEncryptionKey() error = %q, want error containing 'GCP_SECRET_NAME'", err.Error())
		}
	})

	// Note: Testing actual GCP Secret Manager access requires mocking or integration tests
	// For unit tests, we use setTestEncryptionKey() to bypass GCP
}

func TestDecryptInvalidData(t *testing.T) {
	// Set up test encryption key directly (bypassing GCP)
	testKey := generateTestKey()
	resetEncryptionKeyCache()
	if err := setTestEncryptionKey(testKey); err != nil {
		t.Fatalf("Failed to set test encryption key: %v", err)
	}

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
	if err := os.Unsetenv("GCP_SECRET_NAME"); err != nil {
		t.Fatalf("Failed to unset GCP_SECRET_NAME: %v", err)
	}
	resetEncryptionKeyCache()

	_, err := Encrypt("test")
	if err == nil {
		t.Errorf("Encrypt() without GCP_SECRET_NAME should fail")
	}
}

func TestDecryptWithoutKey(t *testing.T) {
	// First encrypt with a key
	testKey := generateTestKey()
	resetEncryptionKeyCache()
	if err := setTestEncryptionKey(testKey); err != nil {
		t.Fatalf("Failed to set test encryption key: %v", err)
	}
	ciphertext, err := Encrypt("test")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// Then try to decrypt without key
	if err := os.Unsetenv("GCP_SECRET_NAME"); err != nil {
		t.Fatalf("Failed to unset GCP_SECRET_NAME: %v", err)
	}
	resetEncryptionKeyCache()

	_, err = Decrypt(ciphertext)
	if err == nil {
		t.Errorf("Decrypt() without GCP_SECRET_NAME should fail")
	}
}

func TestDecryptWithDifferentKey(t *testing.T) {
	// Encrypt with one key
	testKey1 := generateTestKey()
	resetEncryptionKeyCache()
	if err := setTestEncryptionKey(testKey1); err != nil {
		t.Fatalf("Failed to set test encryption key: %v", err)
	}
	ciphertext, err := Encrypt("test")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// Try to decrypt with a different key
	testKey2 := generateTestKey()
	resetEncryptionKeyCache()
	if err := setTestEncryptionKey(testKey2); err != nil {
		t.Fatalf("Failed to set test encryption key: %v", err)
	}

	_, err = Decrypt(ciphertext)
	if err == nil {
		t.Errorf("Decrypt() with different key should fail")
	}
}
