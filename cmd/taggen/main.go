package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/icco/etu-backend/internal/ai"
	"github.com/icco/etu-backend/internal/db"
	"github.com/icco/etu-backend/internal/logger"
	"github.com/icco/etu-backend/internal/storage"
	"golang.org/x/time/rate"
)

func main() {
	log := logger.New()

	// Parse command line flags
	interval := flag.Duration("interval", 0, "Run continuously with this interval (e.g., 1h). If not set, runs once and exits.")
	dryRun := flag.Bool("dry-run", false, "Run without actually adding tags (for testing)")
	delay := flag.Duration("delay", 2*time.Second, "Delay between processing notes to avoid rate limiting (e.g., 2s)")
	flag.Parse()

	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" {
		log.Error("GEMINI_API_KEY environment variable not set")
		os.Exit(1)
	}

	gcsBucket := os.Getenv("GCS_BUCKET")
	if gcsBucket == "" {
		log.Error("GCS_BUCKET environment variable not set")
		os.Exit(1)
	}

	// Initialize AI client
	aiClient, err := ai.NewClient(geminiKey)
	if err != nil {
		log.Error("failed to initialize AI client", "error", err)
		os.Exit(1)
	}

	// Initialize storage client
	ctx := context.Background()
	storageClient, err := storage.New(ctx, gcsBucket)
	if err != nil {
		log.Error("failed to initialize storage client", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := storageClient.Close(); err != nil {
			log.Error("error closing storage client", "error", err)
		}
	}()

	intervalStr := "once"
	if *interval > 0 {
		intervalStr = interval.String()
	}

	log.Info("starting AI processing job (tag generation, OCR, audio transcription)",
		"dry_run", *dryRun,
		"delay", delay.String(),
		"continuous", *interval > 0,
		"interval", intervalStr)

	// Initialize database
	database, err := db.New()
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Error("error closing database", "error", err)
		}
	}()
	log.Info("database connected")

	// Handle graceful shutdown
	processCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Info("received shutdown signal, stopping", "signal", sig.String())
		cancel()
	}()

	if *interval > 0 {
		// Run continuously
		runContinuously(processCtx, log, database, aiClient, storageClient, *dryRun, *delay, *interval)
	} else {
		// Run once
		runOnce(processCtx, log, database, aiClient, storageClient, *dryRun, *delay)
	}
}

func runOnce(ctx context.Context, log *slog.Logger, database *db.DB, aiClient *ai.Client, storageClient *storage.Client, dryRun bool, delay time.Duration) {
	result, err := processAllTasks(ctx, log, database, aiClient, storageClient, dryRun, delay)
	if err != nil {
		log.Error("AI processing failed", "error", err)
		os.Exit(1)
	}

	log.Info("AI processing completed",
		"duration", result.Duration.String(),
		"users_processed", result.UsersProcessed,
		"notes_processed", result.NotesProcessed,
		"tags_added", result.TagsAdded,
		"images_processed", result.ImagesProcessed,
		"audios_processed", result.AudiosProcessed,
		"errors", result.Errors)
}

func runContinuously(ctx context.Context, log *slog.Logger, database *db.DB, aiClient *ai.Client, storageClient *storage.Client, dryRun bool, delay time.Duration, interval time.Duration) {
	// Run immediately on start
	performProcessing(ctx, log, database, aiClient, storageClient, dryRun, delay)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("shutting down AI processing job")
			return
		case <-ticker.C:
			performProcessing(ctx, log, database, aiClient, storageClient, dryRun, delay)
		}
	}
}

func performProcessing(ctx context.Context, log *slog.Logger, database *db.DB, aiClient *ai.Client, storageClient *storage.Client, dryRun bool, delay time.Duration) {
	result, err := processAllTasks(ctx, log, database, aiClient, storageClient, dryRun, delay)
	if err != nil {
		log.Error("AI processing failed", "error", err)
		return
	}

	log.Info("AI processing completed",
		"duration", result.Duration.String(),
		"users_processed", result.UsersProcessed,
		"notes_processed", result.NotesProcessed,
		"tags_added", result.TagsAdded,
		"images_processed", result.ImagesProcessed,
		"audios_processed", result.AudiosProcessed,
		"errors", result.Errors)
}

// ProcessResult holds the results of processing run
type ProcessResult struct {
	UsersProcessed  int
	NotesProcessed  int
	TagsAdded       int
	ImagesProcessed int
	AudiosProcessed int
	Errors          int
	Duration        time.Duration
}

// processAllTasks runs all AI processing tasks in parallel: tag generation, OCR, and audio transcription
func processAllTasks(ctx context.Context, log *slog.Logger, database *db.DB, aiClient *ai.Client, storageClient *storage.Client, dryRun bool, delay time.Duration) (*ProcessResult, error) {
	start := time.Now()
	result := &ProcessResult{}

	// Create a shared rate limiter to control API calls across all tasks
	// The limiter allows one API call per delay period, shared across all goroutines
	var limiter *rate.Limiter
	if delay > 0 {
		limiter = rate.NewLimiter(rate.Every(delay), 1)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex // Protect result updates

	// Task 1: Generate tags for notes
	wg.Add(1)
	go func() {
		defer wg.Done()
		tagResult, err := generateTagsForAllUsers(ctx, log, database, aiClient, dryRun, limiter)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			log.Error("tag generation failed", "error", err)
			result.Errors++
		} else {
			result.UsersProcessed = tagResult.UsersProcessed
			result.NotesProcessed = tagResult.NotesProcessed
			result.TagsAdded = tagResult.TagsAdded
			result.Errors += tagResult.Errors
		}
	}()

	// Task 2: Process images without extracted text
	wg.Add(1)
	go func() {
		defer wg.Done()
		imagesProcessed, imageErrors := processImagesWithoutText(ctx, log, database, aiClient, storageClient, dryRun, limiter)
		mu.Lock()
		defer mu.Unlock()
		result.ImagesProcessed = imagesProcessed
		result.Errors += imageErrors
	}()

	// Task 3: Process audio files without transcription
	wg.Add(1)
	go func() {
		defer wg.Done()
		audiosProcessed, audioErrors := processAudiosWithoutTranscription(ctx, log, database, aiClient, storageClient, dryRun, limiter)
		mu.Lock()
		defer mu.Unlock()
		result.AudiosProcessed = audiosProcessed
		result.Errors += audioErrors
	}()

	// Wait for all tasks to complete
	wg.Wait()

	result.Duration = time.Since(start)
	return result, nil
}

// processImagesWithoutText processes all images that don't have extracted text yet
func processImagesWithoutText(ctx context.Context, log *slog.Logger, database *db.DB, aiClient *ai.Client, storageClient *storage.Client, dryRun bool, limiter *rate.Limiter) (int, int) {
	images, err := database.GetImagesWithoutExtractedText(ctx)
	if err != nil {
		log.Error("failed to get images without extracted text", "error", err)
		return 0, 1
	}

	log.Info("found images without extracted text", "count", len(images))

	processed := 0
	errors := 0

	for _, image := range images {
		select {
		case <-ctx.Done():
			return processed, errors
		default:
		}

		log.Info("processing image for OCR", "image_id", image.ID, "note_id", image.NoteID)

		// Wait for rate limiter before making API call
		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				log.Error("rate limiter error", "error", err)
				return processed, errors
			}
		}

		// Download image from GCS
		imageData, err := storageClient.GetImage(ctx, image.GCSObjectName)
		if err != nil {
			log.Error("failed to download image", "image_id", image.ID, "error", err)
			errors++
			continue
		}

		// Extract text from image
		extractedText, err := aiClient.ExtractTextFromImage(ctx, imageData, image.MimeType)
		if err != nil {
			log.Error("failed to extract text from image", "image_id", image.ID, "error", err)
			errors++
			continue
		}

		log.Info("extracted text from image", "image_id", image.ID, "text_length", len(extractedText))

		if !dryRun {
			// Update database with extracted text
			if err := database.UpdateImageExtractedText(ctx, image.ID, extractedText); err != nil {
				log.Error("failed to update image extracted text", "image_id", image.ID, "error", err)
				errors++
				continue
			}
		}

		processed++
	}

	return processed, errors
}

// processAudiosWithoutTranscription processes all audio files that don't have transcribed text yet
func processAudiosWithoutTranscription(ctx context.Context, log *slog.Logger, database *db.DB, aiClient *ai.Client, storageClient *storage.Client, dryRun bool, limiter *rate.Limiter) (int, int) {
	audios, err := database.GetAudiosWithoutTranscription(ctx)
	if err != nil {
		log.Error("failed to get audios without transcription", "error", err)
		return 0, 1
	}

	log.Info("found audios without transcription", "count", len(audios))

	processed := 0
	errors := 0

	for _, audio := range audios {
		select {
		case <-ctx.Done():
			return processed, errors
		default:
		}

		log.Info("processing audio for transcription", "audio_id", audio.ID, "note_id", audio.NoteID)

		// Wait for rate limiter before making API call
		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				log.Error("rate limiter error", "error", err)
				return processed, errors
			}
		}

		// Download audio from GCS (using GetImage which works for any file type)
		audioData, err := storageClient.GetImage(ctx, audio.GCSObjectName)
		if err != nil {
			log.Error("failed to download audio", "audio_id", audio.ID, "error", err)
			errors++
			continue
		}

		// Transcribe audio
		transcribedText, err := aiClient.TranscribeAudio(ctx, audioData, audio.MimeType)
		if err != nil {
			log.Error("failed to transcribe audio", "audio_id", audio.ID, "error", err)
			errors++
			continue
		}

		log.Info("transcribed audio", "audio_id", audio.ID, "text_length", len(transcribedText))

		if !dryRun {
			// Update database with transcribed text
			if err := database.UpdateAudioTranscribedText(ctx, audio.ID, transcribedText); err != nil {
				log.Error("failed to update audio transcribed text", "audio_id", audio.ID, "error", err)
				errors++
				continue
			}
		}

		processed++
	}

	return processed, errors
}

// generateTagsForAllUsers generates tags for all users in the database
func generateTagsForAllUsers(ctx context.Context, log *slog.Logger, database *db.DB, aiClient *ai.Client, dryRun bool, limiter *rate.Limiter) (*TagGenResult, error) {
	start := time.Now()
	result := &TagGenResult{}

	// Get all users
	users, err := database.ListAllUsers(ctx)
	if err != nil {
		return nil, err
	}

	log.Info("found users to process", "count", len(users))

	for _, user := range users {
		select {
		case <-ctx.Done():
			result.Duration = time.Since(start)
			return result, ctx.Err()
		default:
		}

		userResult, err := generateTagsForUser(ctx, log, database, user.ID, aiClient, dryRun, limiter)
		if err != nil {
			log.Error("failed to generate tags for user", "user_id", user.ID, "error", err)
			result.Errors++
			continue
		}

		result.UsersProcessed++
		result.NotesProcessed += userResult.NotesProcessed
		result.TagsAdded += userResult.TagsAdded
		result.Errors += userResult.Errors
	}

	result.Duration = time.Since(start)
	return result, nil
}

// TagGenResult holds the results of a tag generation run
type TagGenResult struct {
	UsersProcessed int
	NotesProcessed int
	TagsAdded      int
	Errors         int
	Duration       time.Duration
}

func generateTagsForUser(ctx context.Context, log *slog.Logger, database *db.DB, userID string, aiClient *ai.Client, dryRun bool, limiter *rate.Limiter) (*TagGenResult, error) {
	result := &TagGenResult{}

	// Fetch all existing tags for the user to prefer reusing them
	existingTags, err := database.ListTags(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Create a map of existing tag names (lowercase) for easy lookup
	existingTagNames := make(map[string]bool)
	existingTagList := make([]string, 0, len(existingTags))
	for _, tag := range existingTags {
		lowerName := strings.ToLower(tag.Name)
		existingTagNames[lowerName] = true
		existingTagList = append(existingTagList, lowerName)
	}

	// Fetch notes with less than 3 tags
	notes, err := database.GetNotesWithFewTags(ctx, userID, 3)
	if err != nil {
		return nil, err
	}

	log.Info("processing user for tag generation",
		"user_id", userID,
		"notes_with_few_tags", len(notes),
		"existing_tags", len(existingTags))

	for _, note := range notes {
		result.NotesProcessed++

		// Calculate how many tags we can add
		currentTagCount := len(note.Tags)
		maxNewTags := 3 - currentTagCount

		if maxNewTags <= 0 {
			continue
		}

		// Wait for rate limiter before making API call
		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				log.Error("rate limiter error", "error", err)
				return result, err
			}
		}

		// Generate tags using Gemini, passing existing tags
		generatedTags, err := aiClient.GenerateTags(ctx, note.Content, existingTagList)
		if err != nil {
			log.Error("failed to generate tags for note", "note_id", note.ID, "error", err)
			result.Errors++
			continue
		}

		// Filter out tags that already exist on this note
		var newTags []string
		existingNoteTagNames := make(map[string]bool)
		for _, tag := range note.Tags {
			existingNoteTagNames[strings.ToLower(tag.Name)] = true
		}

		// Prefer existing tags over new ones
		var preferredTags []string
		var otherTags []string

		for _, tag := range generatedTags {
			tag = strings.ToLower(tag)
			if existingNoteTagNames[tag] {
				// Skip tags already on this note
				continue
			}
			if existingTagNames[tag] {
				preferredTags = append(preferredTags, tag)
			} else {
				otherTags = append(otherTags, tag)
			}
		}

		// Add preferred tags first, then other tags
		newTags = append(newTags, preferredTags...)
		newTags = append(newTags, otherTags...)

		// Limit to maxNewTags
		if len(newTags) > maxNewTags {
			newTags = newTags[:maxNewTags]
		}

		if len(newTags) == 0 {
			continue
		}

		log.Info("adding tags to note",
			"note_id", note.ID,
			"new_tags", newTags,
			"count", len(newTags),
			"dry_run", dryRun)

		if !dryRun {
			// Add tags to the note
			if err := database.AddTagsToNote(ctx, userID, note.ID, newTags); err != nil {
				log.Error("failed to add tags to note", "note_id", note.ID, "error", err)
				result.Errors++
				continue
			}
		}

		result.TagsAdded += len(newTags)
	}

	return result, nil
}
