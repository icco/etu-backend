package ai

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// ExtractTextFromImage uses Gemini's vision capabilities to extract text from an image.
// imageData is the raw image bytes, mimeType is the MIME type (e.g., "image/jpeg", "image/png").
// Returns the extracted text, or an empty string if no text is found.
func (c *Client) ExtractTextFromImage(ctx context.Context, imageData []byte, mimeType string) (string, error) {
	if len(imageData) == 0 {
		return "", fmt.Errorf("image data is empty")
	}

	// Validate mime type
	if !isValidImageMimeType(mimeType) {
		return "", fmt.Errorf("unsupported image MIME type: %s", mimeType)
	}

	client, err := c.newGenaiClient(ctx)
	if err != nil {
		return "", err
	}

	// Create content with both text prompt and image
	prompt := "Extract all text from this image. Return only the extracted text exactly as it appears, preserving line breaks and formatting. If there is no text in the image, respond with an empty string."

	// Build the content with image and text
	content := &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{
				InlineData: &genai.Blob{
					MIMEType: mimeType,
					Data:     imageData,
				},
			},
			{
				Text: prompt,
			},
		},
	}

	resp, err := client.Models.GenerateContent(ctx, "gemini-2.0-flash", []*genai.Content{content}, &genai.GenerateContentConfig{
		Temperature: genai.Ptr(float32(0.1)), // Very low temperature for accurate extraction
	})
	if err != nil {
		return "", fmt.Errorf("failed to extract text from image: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", nil // No text found
	}

	// Collect all text parts from the response
	var extractedText strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			extractedText.WriteString(part.Text)
		}
	}

	return strings.TrimSpace(extractedText.String()), nil
}

// isValidImageMimeType checks if the MIME type is a supported image format.
func isValidImageMimeType(mimeType string) bool {
	supportedTypes := map[string]bool{
		"image/jpeg": true,
		"image/jpg":  true,
		"image/png":  true,
		"image/gif":  true,
		"image/webp": true,
		"image/heic": true,
		"image/heif": true,
	}
	return supportedTypes[strings.ToLower(mimeType)]
}
