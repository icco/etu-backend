package ai

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// GenerateTags generates a list of lowercase, single-word tags for a given text using Gemini.
// It returns up to 3 tags.
func GenerateTags(ctx context.Context, text string, apiKey string) ([]string, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("no Gemini API key configured")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	// Use Gemini Flash for cost-effectiveness
	prompt := fmt.Sprintf(`Given the journal entry below, generate up to 3 single-word tags to summarize the content. Each tag should be:
- A single word (no spaces, no hyphens)
- Lowercase
- Relevant to the content

Output should be a comma-separated list of tags only, no other text.

Journal entry:
%s`, text)

	resp, err := client.Models.GenerateContent(ctx, "gemini-1.5-flash-8b", []*genai.Content{
		genai.NewContentFromText(prompt, genai.RoleUser),
	}, &genai.GenerateContentConfig{
		Temperature: genai.Ptr(float32(0.3)), // Lower temperature for more consistent results
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate tags: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no response from Gemini")
	}

	// Extract text from response
	var tags []string
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			outText := part.Text
			// Split by comma and clean up
			rawTags := strings.Split(outText, ",")
			for _, tag := range rawTags {
				tag = strings.TrimSpace(tag)
				tag = strings.ToLower(tag)
				// Only accept single words (no spaces)
				if tag != "" && !strings.Contains(tag, " ") {
					tags = append(tags, tag)
				}
			}
		}
	}

	// Limit to 3 tags maximum
	if len(tags) > 3 {
		tags = tags[:3]
	}

	return tags, nil
}
