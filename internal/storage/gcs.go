package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"cloud.google.com/go/storage"
)

// Client wraps the GCS client for image storage operations.
type Client struct {
	client *storage.Client
	bucket string
}

// New creates a new GCS storage client.
// The bucket parameter specifies the GCS bucket to use for storage.
// Uses Application Default Credentials for authentication.
func New(ctx context.Context, bucket string) (*Client, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return &Client{
		client: client,
		bucket: bucket,
	}, nil
}

// Close closes the GCS client connection.
func (c *Client) Close() error {
	return c.client.Close()
}

// SignedURLDuration is how long signed URLs remain valid
const SignedURLDuration = 7 * 24 * time.Hour // 7 days

// UploadImage uploads image data to GCS and returns a signed URL for access.
// objectName should be a unique identifier for the image (e.g., "notes/{noteID}/{imageID}").
// mimeType should be the MIME type of the image (e.g., "image/jpeg", "image/png").
func (c *Client) UploadImage(ctx context.Context, objectName string, data []byte, mimeType string) (string, error) {
	obj := c.client.Bucket(c.bucket).Object(objectName)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	writer := obj.NewWriter(ctx)
	writer.ContentType = mimeType
	writer.CacheControl = "private, max-age=3600" // Cache for 1 hour, private since we use signed URLs

	if _, err := writer.Write(data); err != nil {
		return "", fmt.Errorf("failed to write image data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close writer: %w", err)
	}

	// Generate a signed URL for accessing the object
	url, err := c.GetSignedURL(ctx, objectName)
	if err != nil {
		return "", fmt.Errorf("failed to generate signed URL: %w", err)
	}

	return url, nil
}

// GetSignedURL generates a signed URL for accessing an object.
// The URL is valid for SignedURLDuration.
func (c *Client) GetSignedURL(ctx context.Context, objectName string) (string, error) {
	opts := &storage.SignedURLOptions{
		Scheme:  storage.SigningSchemeV4,
		Method:  "GET",
		Expires: time.Now().Add(SignedURLDuration),
	}

	url, err := c.client.Bucket(c.bucket).SignedURL(objectName, opts)
	if err != nil {
		return "", fmt.Errorf("failed to create signed URL: %w", err)
	}

	return url, nil
}

// DeleteImage deletes an image from GCS.
func (c *Client) DeleteImage(ctx context.Context, objectName string) error {
	obj := c.client.Bucket(c.bucket).Object(objectName)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := obj.Delete(ctx); err != nil {
		if err == storage.ErrObjectNotExist {
			// Object doesn't exist, nothing to delete
			return nil
		}
		return fmt.Errorf("failed to delete image: %w", err)
	}

	return nil
}

// GetImage retrieves image data from GCS.
func (c *Client) GetImage(ctx context.Context, objectName string) (data []byte, err error) {
	obj := c.client.Bucket(c.bucket).Object(objectName)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	reader, err := obj.NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create reader: %w", err)
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close reader: %w", closeErr)
		}
	}()

	data, err = io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read image data: %w", err)
	}

	return data, nil
}

// Bucket returns the bucket name.
func (c *Client) Bucket() string {
	return c.bucket
}
