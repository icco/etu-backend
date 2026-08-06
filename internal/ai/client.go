package ai

import (
	"context"
	"fmt"

	"github.com/icco/gutil/vertex"
)

// model is the Gemini model used for every call in this package. Flash-lite is
// enough for OCR, transcription, and three-word tagging, and is the cheapest
// option that handles inline media.
const model = "gemini-2.5-flash-lite"

// Client wraps the Gemini API client with shared configuration
type Client struct {
	project  string
	location string
}

// NewClient creates a new AI client using Vertex AI with Application Default Credentials.
// project is the GCP project ID; location defaults to vertex.DefaultLocation
// if empty.
func NewClient(project, location string) (*Client, error) {
	if project == "" {
		return nil, fmt.Errorf("GCP project is required")
	}
	if location == "" {
		location = vertex.DefaultLocation
	}
	return &Client{
		project:  project,
		location: location,
	}, nil
}

// newVertexClient creates a Gemini client via Vertex AI.
// Note: Creates a new client for each call. If performance becomes an issue,
// consider caching the client in the Client struct. However, the genai library
// manages connection pooling internally, so this approach is acceptable for now.
func (c *Client) newVertexClient(ctx context.Context) (*vertex.Client, error) {
	client, err := vertex.New(ctx, vertex.Config{
		Project:  c.project,
		Location: c.location,
		Model:    model,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	return client, nil
}
