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
	"github.com/icco/gutil/logging"
	"go.uber.org/zap"
)

var (
	encryptionKey     []byte
	encryptionKeyErr  error
	encryptionKeyOnce sync.Once
	keyErrLogOnce     sync.Once
)

// DecryptOrPlaintext decrypts a stored secret, returning it unchanged if it
// was never encrypted.
//
// An unavailable encryption key is a process-wide configuration fault, not
// evidence that this particular value is plaintext, so it is reported once at
// error level instead of once per value.
func DecryptOrPlaintext(ctx context.Context, stored string) string {
	if stored == "" {
		return ""
	}

	if _, err := GetEncryptionKey(); err != nil {
		keyErrLogOnce.Do(func() {
			logging.FromContext(ctx).Errorw("encryption key unavailable, storing and reading secrets as plaintext", zap.Error(err))
		})
		return stored
	}

	decrypted, err := Decrypt(stored)
	if err != nil {
		logging.FromContext(ctx).Debugw("secret is not ciphertext, using as plaintext", zap.Error(err))
		return stored
	}

	return decrypted
}

// GetEncryptionKey retrieves the encryption key from GCP Secret Manager.
// The key should be a base64-encoded 32-byte key for AES-256.
// Results are cached after the first call.
func GetEncryptionKey() ([]byte, error) {
	encryptionKeyOnce.Do(func() {
		secretName := os.Getenv("GCP_SECRET_NAME")
		if secretName == "" {
			encryptionKeyErr = errors.New("GCP_SECRET_NAME environment variable not set")
			return
		}

		encryptionKey, encryptionKeyErr = getKeyFromGCP(secretName)
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
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			// Log error but don't fail since we already have the result
			fmt.Fprintf(os.Stderr, "warning: failed to close GCP Secret Manager client: %v\n", closeErr)
		}
	}()

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
