package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/icco/etu-backend/internal/ai"
	"github.com/icco/etu-backend/internal/db"
	"github.com/icco/etu-backend/internal/logger"
	"github.com/icco/etu-backend/internal/storage"
	"github.com/icco/etu-backend/internal/tagging"
	"github.com/icco/gutil/logging"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

func main() {
	log := logger.New()

	// rootCtx carries the application logger so descendant contexts
	// (signal-cancellable, per-task) can retrieve it via logging.FromContext.
	rootCtx := logging.NewContext(context.Background(), log)

	interval := flag.Duration("interval", 0, "Run continuously with this interval (e.g., 1h). If not set, runs once and exits.")
	dryRun := flag.Bool("dry-run", false, "Run without actually adding tags (for testing)")
	flag.Parse()

	geminiProject := os.Getenv("GEMINI_PROJECT")
	if geminiProject == "" {
		log.Errorw("GEMINI_PROJECT environment variable not set")
		os.Exit(1)
	}

	gcsBucket := os.Getenv("GCS_BUCKET")
	if gcsBucket == "" {
		log.Errorw("GCS_BUCKET environment variable not set")
		os.Exit(1)
	}

	aiClient, err := ai.NewClient(geminiProject, os.Getenv("GEMINI_LOCATION"))
	if err != nil {
		log.Errorw("failed to initialize AI client", zap.Error(err))
		os.Exit(1)
	}

	storageClient, err := storage.New(rootCtx, gcsBucket)
	if err != nil {
		log.Errorw("failed to initialize storage client", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		if err := storageClient.Close(); err != nil {
			log.Errorw("error closing storage client", zap.Error(err))
		}
	}()

	intervalStr := "once"
	if *interval > 0 {
		intervalStr = interval.String()
	}

	log.Infow("starting AI processing job (tag generation, OCR, audio transcription)",
		"dry_run", *dryRun,
		"continuous", *interval > 0,
		"interval", intervalStr)

	database, err := db.New()
	if err != nil {
		log.Errorw("failed to connect to database", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Errorw("error closing database", zap.Error(err))
		}
	}()
	log.Infow("database connected")

	processCtx, cancel := context.WithCancel(rootCtx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Infow("received shutdown signal, stopping", "signal", sig.String())
		cancel()
	}()

	// 1 API call per second shared across all tasks.
	rateLimiter := rate.NewLimiter(rate.Every(1*time.Second), 1)

	if *interval > 0 {
		ticker := time.NewTicker(*interval)
		defer ticker.Stop()

		processOnce(processCtx, database, aiClient, storageClient, *dryRun, rateLimiter)

		for {
			select {
			case <-processCtx.Done():
				log.Infow("shutting down AI processing job")
				return
			case <-ticker.C:
				processOnce(processCtx, database, aiClient, storageClient, *dryRun, rateLimiter)
			}
		}
	} else {
		processOnce(processCtx, database, aiClient, storageClient, *dryRun, rateLimiter)
	}
}

func processOnce(ctx context.Context, database *db.DB, aiClient *ai.Client, storageClient *storage.Client, dryRun bool, rateLimiter *rate.Limiter) {
	l := logging.FromContext(ctx)

	result, err := processAllTasks(ctx, database, aiClient, storageClient, dryRun, rateLimiter)
	if err != nil {
		l.Errorw("AI processing failed", zap.Error(err))
		return
	}

	l.Infow("AI processing completed",
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
func processAllTasks(ctx context.Context, database *db.DB, aiClient *ai.Client, storageClient *storage.Client, dryRun bool, rateLimiter *rate.Limiter) (*ProcessResult, error) {
	l := logging.FromContext(ctx)

	start := time.Now()
	result := &ProcessResult{}

	var wg sync.WaitGroup
	var mu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		tagResult, err := generateTagsForAllUsers(ctx, database, aiClient, dryRun, rateLimiter)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			l.Errorw("tag generation failed", zap.Error(err))
			result.Errors++
		} else {
			result.UsersProcessed = tagResult.UsersProcessed
			result.NotesProcessed = tagResult.NotesProcessed
			result.TagsAdded = tagResult.TagsAdded
			result.Errors += tagResult.Errors
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		imagesProcessed, imageErrors := processImagesWithoutText(ctx, database, aiClient, storageClient, dryRun, rateLimiter)
		mu.Lock()
		defer mu.Unlock()
		result.ImagesProcessed = imagesProcessed
		result.Errors += imageErrors
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		audiosProcessed, audioErrors := processAudiosWithoutTranscription(ctx, database, aiClient, storageClient, dryRun, rateLimiter)
		mu.Lock()
		defer mu.Unlock()
		result.AudiosProcessed = audiosProcessed
		result.Errors += audioErrors
	}()

	wg.Wait()

	result.Duration = time.Since(start)
	return result, nil
}

// processImagesWithoutText processes all images that don't have extracted text yet
func processImagesWithoutText(ctx context.Context, database *db.DB, aiClient *ai.Client, storageClient *storage.Client, dryRun bool, limiter *rate.Limiter) (int, int) {
	l := logging.FromContext(ctx)

	images, err := database.GetImagesWithoutExtractedText(ctx)
	if err != nil {
		l.Errorw("failed to get images without extracted text", zap.Error(err))
		return 0, 1
	}

	l.Infow("found images without extracted text", "count", len(images))

	processed := 0
	errors := 0

	for _, image := range images {
		select {
		case <-ctx.Done():
			return processed, errors
		default:
		}

		l.Infow("processing image for OCR", "image_id", image.ID, "note_id", image.NoteID)

		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				l.Errorw("rate limiter error", zap.Error(err))
				return processed, errors
			}
		}

		imageData, err := storageClient.GetImage(ctx, image.GCSObjectName)
		if err != nil {
			l.Errorw("failed to download image", "image_id", image.ID, zap.Error(err))
			errors++
			continue
		}

		extractedText, err := aiClient.ExtractTextFromImage(ctx, imageData, image.MimeType)
		if err != nil {
			l.Errorw("failed to extract text from image", "image_id", image.ID, zap.Error(err))
			errors++
			continue
		}

		l.Infow("extracted text from image", "image_id", image.ID, "text_length", len(extractedText))

		if !dryRun {
			if err := database.UpdateImageExtractedText(ctx, image.ID, extractedText); err != nil {
				l.Errorw("failed to update image extracted text", "image_id", image.ID, zap.Error(err))
				errors++
				continue
			}
		}

		processed++
	}

	return processed, errors
}

// processAudiosWithoutTranscription processes all audio files that don't have transcribed text yet
func processAudiosWithoutTranscription(ctx context.Context, database *db.DB, aiClient *ai.Client, storageClient *storage.Client, dryRun bool, limiter *rate.Limiter) (int, int) {
	l := logging.FromContext(ctx)

	audios, err := database.GetAudiosWithoutTranscription(ctx)
	if err != nil {
		l.Errorw("failed to get audios without transcription", zap.Error(err))
		return 0, 1
	}

	l.Infow("found audios without transcription", "count", len(audios))

	processed := 0
	errors := 0

	for _, audio := range audios {
		select {
		case <-ctx.Done():
			return processed, errors
		default:
		}

		l.Infow("processing audio for transcription", "audio_id", audio.ID, "note_id", audio.NoteID)

		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				l.Errorw("rate limiter error", zap.Error(err))
				return processed, errors
			}
		}

		// GetImage works for any file type stored in GCS.
		audioData, err := storageClient.GetImage(ctx, audio.GCSObjectName)
		if err != nil {
			l.Errorw("failed to download audio", "audio_id", audio.ID, zap.Error(err))
			errors++
			continue
		}

		transcribedText, err := aiClient.TranscribeAudio(ctx, audioData, audio.MimeType)
		if err != nil {
			l.Errorw("failed to transcribe audio", "audio_id", audio.ID, zap.Error(err))
			errors++
			continue
		}

		l.Infow("transcribed audio", "audio_id", audio.ID, "text_length", len(transcribedText))

		if !dryRun {
			if err := database.UpdateAudioTranscribedText(ctx, audio.ID, transcribedText); err != nil {
				l.Errorw("failed to update audio transcribed text", "audio_id", audio.ID, zap.Error(err))
				errors++
				continue
			}
		}

		processed++
	}

	return processed, errors
}

// generateTagsForAllUsers generates tags for all users in the database
func generateTagsForAllUsers(ctx context.Context, database *db.DB, aiClient *ai.Client, dryRun bool, limiter *rate.Limiter) (*TagGenResult, error) {
	l := logging.FromContext(ctx)

	start := time.Now()
	result := &TagGenResult{}

	users, err := database.ListAllUsers(ctx)
	if err != nil {
		return nil, err
	}

	l.Infow("found users to process", "count", len(users))

	for _, user := range users {
		select {
		case <-ctx.Done():
			result.Duration = time.Since(start)
			return result, ctx.Err()
		default:
		}

		userResult, err := generateTagsForUser(ctx, database, user.ID, aiClient, dryRun, limiter)
		if err != nil {
			l.Errorw("failed to generate tags for user", "user_id", user.ID, zap.Error(err))
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

func generateTagsForUser(ctx context.Context, database *db.DB, userID string, aiClient *ai.Client, dryRun bool, limiter *rate.Limiter) (*TagGenResult, error) {
	l := logging.FromContext(ctx).With("user_id", userID)

	result := &TagGenResult{}

	existingTags, err := database.ListTags(ctx, userID)
	if err != nil {
		return nil, err
	}

	existingTagValues := make([]string, 0, len(existingTags))
	for _, tag := range existingTags {
		existingTagValues = append(existingTagValues, tag.Name)
	}
	existingTagNames, existingTagList := tagging.BuildExistingTagContext(existingTagValues)

	notes, err := database.GetNotesWithFewTags(ctx, userID, 3)
	if err != nil {
		return nil, err
	}

	l.Infow("processing user for tag generation",
		"notes_with_few_tags", len(notes),
		"existing_tags", len(existingTags))

	for _, note := range notes {
		result.NotesProcessed++

		currentTagCount := len(note.Tags)
		maxNewTags := 3 - currentTagCount

		if maxNewTags <= 0 {
			continue
		}

		existingNoteTagValues := make([]string, 0, len(note.Tags))
		for _, tag := range note.Tags {
			existingNoteTagValues = append(existingNoteTagValues, tag.Name)
		}
		existingNoteTagNames := tagging.BuildExistingTagSet(existingNoteTagValues)

		hashtagsToAdd := tagging.SelectHashtagsToAdd(note.Content, existingNoteTagNames, maxNewTags)

		if len(hashtagsToAdd) > 0 {
			l.Infow("adding hashtags to note",
				"note_id", note.ID,
				"hashtags", hashtagsToAdd,
				"dry_run", dryRun)

			if !dryRun {
				if err := database.AddTagsToNote(ctx, userID, note.ID, hashtagsToAdd); err != nil {
					l.Errorw("failed to add hashtags to note", "note_id", note.ID, zap.Error(err))
					result.Errors++
					continue
				}
			}
			result.TagsAdded += len(hashtagsToAdd)
			maxNewTags -= len(hashtagsToAdd)
		}

		if maxNewTags <= 0 {
			continue
		}

		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				l.Errorw("rate limiter error", zap.Error(err))
				return result, err
			}
		}

		generatedTags, err := aiClient.GenerateTags(ctx, note.Content, existingTagList)
		if err != nil {
			l.Errorw("failed to generate tags for note", "note_id", note.ID, zap.Error(err))
			result.Errors++
			continue
		}

		newTags := tagging.SelectGeneratedTags(generatedTags, existingNoteTagNames, existingTagNames, maxNewTags)

		if len(newTags) == 0 {
			continue
		}

		l.Infow("adding tags to note",
			"note_id", note.ID,
			"new_tags", newTags,
			"count", len(newTags),
			"dry_run", dryRun)

		if !dryRun {
			if err := database.AddTagsToNote(ctx, userID, note.ID, newTags); err != nil {
				l.Errorw("failed to add tags to note", "note_id", note.ID, zap.Error(err))
				result.Errors++
				continue
			}
		}

		result.TagsAdded += len(newTags)
	}

	return result, nil
}
