package ai

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

// Client wraps the Gemini API client with shared configuration
type Client struct {
	project  string
	location string
}

// NewClient creates a new AI client using Vertex AI with Application Default Credentials.
// project is the GCP project ID; location defaults to "us-central1" if empty.
func NewClient(project, location string) (*Client, error) {
	if project == "" {
		return nil, fmt.Errorf("GCP project is required")
	}
	if location == "" {
		location = "us-central1"
	}
	return &Client{
		project:  project,
		location: location,
	}, nil
}

// newGenaiClient creates a new Gemini client via Vertex AI.
// Note: Creates a new client for each call. If performance becomes an issue,
// consider caching the client in the Client struct. However, the genai library
// manages connection pooling internally, so this approach is acceptable for now.
func (c *Client) newGenaiClient(ctx context.Context) (*genai.Client, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  c.project,
		Location: c.location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	return client, nil
}
