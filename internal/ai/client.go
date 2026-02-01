package ai

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

// Client wraps the Gemini API client with shared configuration
type Client struct {
	apiKey string
}

// NewClient creates a new AI client with the provided API key
func NewClient(apiKey string) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	return &Client{
		apiKey: apiKey,
	}, nil
}

// newGenaiClient creates a new Gemini API client
// Note: Creates a new client for each call. If performance becomes an issue,
// consider caching the client in the Client struct. However, the genai library
// manages connection pooling internally, so this approach is acceptable for now.
func (c *Client) newGenaiClient(ctx context.Context) (*genai.Client, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: c.apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	return client, nil
}
