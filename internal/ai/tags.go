package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/genai"
)

// GenerateTags generates a list of lowercase, single-word tags for a given text using Gemini.
// It returns up to 3 tags. existingTags is a list of tags the user has previously used.
func GenerateTags(ctx context.Context, text string, existingTags []string, apiKey string) ([]string, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("no Gemini API key configured")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	// Build the existing tags list for the prompt
	existingTagsStr := ""
	if len(existingTags) > 0 {
		existingTagsStr = fmt.Sprintf("\n\nThe user has previously used these tags (prefer reusing these if relevant): %s", strings.Join(existingTags, ", "))
	}

	// Use Gemini Flash for cost-effectiveness
	prompt := fmt.Sprintf(`Given the journal entry below, generate up to 3 single-word tags to summarize the content. Each tag should be:
- A single word (no spaces, no hyphens, only alphanumeric characters)
- Lowercase
- Relevant to the content%s

Return ONLY a JSON array of strings, nothing else. Example: ["tag1", "tag2", "tag3"]

Journal entry:
%s`, existingTagsStr, text)

	resp, err := client.Models.GenerateContent(ctx, "gemini-1.5-flash-8b", []*genai.Content{
		genai.NewContentFromText(prompt, genai.RoleUser),
	}, &genai.GenerateContentConfig{
		Temperature:      genai.Ptr(float32(0.3)), // Lower temperature for more consistent results
		ResponseMIMEType: "application/json",
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
			// Try to parse as JSON array
			var jsonTags []string
			if err := json.Unmarshal([]byte(part.Text), &jsonTags); err == nil {
				// Successfully parsed JSON
				for _, tag := range jsonTags {
					tag = strings.TrimSpace(tag)
					tag = strings.ToLower(tag)
					// Only accept single words (alphanumeric only)
					if tag != "" && isValidTag(tag) {
						tags = append(tags, tag)
					}
				}
			} else {
				// Fallback to comma-separated parsing if JSON parsing fails
				rawTags := strings.Split(part.Text, ",")
				for _, tag := range rawTags {
					tag = strings.TrimSpace(tag)
					tag = strings.ToLower(tag)
					// Remove any quotes or brackets
					tag = strings.Trim(tag, "\"'[]")
					tag = strings.TrimSpace(tag)
					// Only accept single words (alphanumeric only)
					if tag != "" && isValidTag(tag) {
						tags = append(tags, tag)
					}
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

var tagRegex = regexp.MustCompile(`^[a-z0-9]+$`)

// isValidTag checks if a tag is valid (alphanumeric lowercase only)
func isValidTag(s string) bool {
	return tagRegex.MatchString(s)
}
