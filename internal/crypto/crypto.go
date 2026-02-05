package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

var (
	encryptionKey     []byte
	encryptionKeyErr  error
	encryptionKeyOnce sync.Once
)

// GetEncryptionKey retrieves the encryption key from GCP Secret Manager or environment.
// Priority: GCP_SECRET_NAME > ENCRYPTION_KEY environment variable
// The key should be a base64-encoded 32-byte key for AES-256.
// Results are cached after the first call.
func GetEncryptionKey() ([]byte, error) {
	encryptionKeyOnce.Do(func() {
		// Try GCP Secret Manager first
		secretName := os.Getenv("GCP_SECRET_NAME")
		if secretName != "" {
			encryptionKey, encryptionKeyErr = getKeyFromGCP(secretName)
			if encryptionKeyErr == nil {
				return
			}
			// Log error but fall through to environment variable
		}

		// Fall back to environment variable
		key := os.Getenv("ENCRYPTION_KEY")
		if key == "" {
			encryptionKeyErr = errors.New("neither GCP_SECRET_NAME nor ENCRYPTION_KEY environment variable set")
			return
		}

		// Decode from base64
		decoded, err := base64.StdEncoding.DecodeString(key)
		if err != nil {
			encryptionKeyErr = fmt.Errorf("failed to decode ENCRYPTION_KEY: %w", err)
			return
		}

		// Verify key is 32 bytes for AES-256
		if len(decoded) != 32 {
			encryptionKeyErr = fmt.Errorf("ENCRYPTION_KEY must be 32 bytes (256 bits), got %d bytes", len(decoded))
			return
		}

		encryptionKey = decoded
	})

	return encryptionKey, encryptionKeyErr
}

// getKeyFromGCP retrieves the encryption key from GCP Secret Manager.
// secretName should be in format: projects/PROJECT_ID/secrets/SECRET_NAME/versions/VERSION
// or projects/PROJECT_ID/secrets/SECRET_NAME (uses latest version)
func getKeyFromGCP(secretName string) ([]byte, error) {
	ctx := context.Background()

	// Create the client
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCP Secret Manager client: %w", err)
	}
	defer client.Close()

	// Build the request
	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretName,
	}

	// Access the secret
	result, err := client.AccessSecretVersion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to access secret %q: %w", secretName, err)
	}

	// Decode from base64
	decoded, err := base64.StdEncoding.DecodeString(string(result.Payload.Data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode secret from base64: %w", err)
	}

	// Verify key is 32 bytes for AES-256
	if len(decoded) != 32 {
		return nil, fmt.Errorf("secret must be 32 bytes (256 bits), got %d bytes", len(decoded))
	}

	return decoded, nil
}

// Encrypt encrypts plaintext using AES-256-GCM.
// Returns base64-encoded ciphertext with nonce prepended.
func Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	key, err := GetEncryptionKey()
	if err != nil {
		return "", err
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt the plaintext
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	// Encode to base64 for storage
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts base64-encoded ciphertext using AES-256-GCM.
// The nonce is expected to be prepended to the ciphertext.
func Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}

	key, err := GetEncryptionKey()
	if err != nil {
		return "", err
	}

	// Decode from base64
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Extract nonce
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}
