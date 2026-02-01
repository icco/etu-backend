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
func ExtractTextFromImage(ctx context.Context, imageData []byte, mimeType string, apiKey string) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("no Gemini API key configured")
	}

	if len(imageData) == 0 {
		return "", fmt.Errorf("image data is empty")
	}

	// Basic validation - must be an image type. Let Gemini return specific errors
	// for unsupported image formats rather than maintaining our own allowlist.
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return "", fmt.Errorf("not an image MIME type: %s", mimeType)
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: apiKey,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create Gemini client: %w", err)
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
