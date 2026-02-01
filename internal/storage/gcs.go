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

// UploadImage uploads image data to GCS and returns the public URL.
// objectName should be a unique identifier for the image (e.g., "notes/{noteID}/{imageID}").
// mimeType should be the MIME type of the image (e.g., "image/jpeg", "image/png").
func (c *Client) UploadImage(ctx context.Context, objectName string, data []byte, mimeType string) (string, error) {
	obj := c.client.Bucket(c.bucket).Object(objectName)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	writer := obj.NewWriter(ctx)
	writer.ContentType = mimeType
	writer.CacheControl = "public, max-age=31536000" // Cache for 1 year

	if _, err := writer.Write(data); err != nil {
		return "", fmt.Errorf("failed to write image data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close writer: %w", err)
	}

	// Return the public URL for the object
	url := fmt.Sprintf("https://storage.googleapis.com/%s/%s", c.bucket, objectName)
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
func (c *Client) GetImage(ctx context.Context, objectName string) ([]byte, error) {
	obj := c.client.Bucket(c.bucket).Object(objectName)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	reader, err := obj.NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create reader: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read image data: %w", err)
	}

	return data, nil
}

// Bucket returns the bucket name.
func (c *Client) Bucket() string {
	return c.bucket
}
