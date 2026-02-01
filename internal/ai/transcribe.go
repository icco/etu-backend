package ai

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// TranscribeAudio uses Gemini's audio capabilities to transcribe audio files.
// audioData is the raw audio bytes, mimeType is the MIME type (e.g., "audio/mpeg", "audio/wav").
// Returns the transcribed text, or an empty string if transcription fails.
func (c *Client) TranscribeAudio(ctx context.Context, audioData []byte, mimeType string) (string, error) {
	if len(audioData) == 0 {
		return "", fmt.Errorf("audio data is empty")
	}

	// Validate mime type
	if !isValidAudioMimeType(mimeType) {
		return "", fmt.Errorf("unsupported audio MIME type: %s", mimeType)
	}

	client, err := c.newGenaiClient(ctx)
	if err != nil {
		return "", err
	}

	// Create content with both text prompt and audio
	prompt := "Transcribe this audio file. Return only the transcribed text exactly as spoken. If there is no speech in the audio, respond with an empty string."

	// Build the content with audio and text
	content := &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{
				InlineData: &genai.Blob{
					MIMEType: mimeType,
					Data:     audioData,
				},
			},
			{
				Text: prompt,
			},
		},
	}

	resp, err := client.Models.GenerateContent(ctx, "gemini-2.0-flash", []*genai.Content{content}, &genai.GenerateContentConfig{
		Temperature: genai.Ptr(float32(0.1)), // Very low temperature for accurate transcription
	})
	if err != nil {
		return "", fmt.Errorf("failed to transcribe audio: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", nil // No transcription found
	}

	// Collect all text parts from the response
	var transcribedText strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			transcribedText.WriteString(part.Text)
		}
	}

	return strings.TrimSpace(transcribedText.String()), nil
}

// isValidAudioMimeType checks if the MIME type is a supported audio format.
func isValidAudioMimeType(mimeType string) bool {
	supportedTypes := map[string]bool{
		"audio/mpeg": true,  // MP3
		"audio/mp3":  true,  // MP3 (alternative)
		"audio/wav":  true,  // WAV
		"audio/wave": true,  // WAV (alternative)
		"audio/ogg":  true,  // OGG
		"audio/webm": true,  // WebM
		"audio/mp4":  true,  // MP4 audio
		"audio/m4a":  true,  // M4A
		"audio/flac": true,  // FLAC
		"audio/aac":  true,  // AAC
	}
	return supportedTypes[strings.ToLower(mimeType)]
}
