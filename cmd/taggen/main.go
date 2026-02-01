package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/icco/etu-backend/internal/ai"
	"github.com/icco/etu-backend/internal/db"
	"github.com/icco/etu-backend/internal/logger"
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

	intervalStr := "once"
	if *interval > 0 {
		intervalStr = interval.String()
	}

	log.Info("starting Gemini tag generation job",
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
	ctx, cancel := context.WithCancel(context.Background())
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
		runContinuously(ctx, log, database, geminiKey, *dryRun, *delay, *interval)
	} else {
		// Run once
		runOnce(ctx, log, database, geminiKey, *dryRun, *delay)
	}
}

func runOnce(ctx context.Context, log *slog.Logger, database *db.DB, geminiKey string, dryRun bool, delay time.Duration) {
	result, err := generateTagsForAllUsers(ctx, log, database, geminiKey, dryRun, delay)
	if err != nil {
		log.Error("tag generation failed", "error", err)
		os.Exit(1)
	}

	log.Info("tag generation completed",
		"duration", result.Duration.String(),
		"users_processed", result.UsersProcessed,
		"notes_processed", result.NotesProcessed,
		"tags_added", result.TagsAdded,
		"errors", result.Errors)
}

func runContinuously(ctx context.Context, log *slog.Logger, database *db.DB, geminiKey string, dryRun bool, delay time.Duration, interval time.Duration) {
	// Run immediately on start
	performTagGeneration(ctx, log, database, geminiKey, dryRun, delay)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("shutting down tag generation job")
			return
		case <-ticker.C:
			performTagGeneration(ctx, log, database, geminiKey, dryRun, delay)
		}
	}
}

func performTagGeneration(ctx context.Context, log *slog.Logger, database *db.DB, geminiKey string, dryRun bool, delay time.Duration) {
	result, err := generateTagsForAllUsers(ctx, log, database, geminiKey, dryRun, delay)
	if err != nil {
		log.Error("tag generation failed", "error", err)
		return
	}

	log.Info("tag generation completed",
		"duration", result.Duration.String(),
		"users_processed", result.UsersProcessed,
		"notes_processed", result.NotesProcessed,
		"tags_added", result.TagsAdded,
		"errors", result.Errors)
}

// generateTagsForAllUsers generates tags for all users in the database
func generateTagsForAllUsers(ctx context.Context, log *slog.Logger, database *db.DB, geminiKey string, dryRun bool, delay time.Duration) (*TagGenResult, error) {
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

		userResult, err := generateTagsForUser(ctx, log, database, user.ID, geminiKey, dryRun, delay)
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

func generateTagsForUser(ctx context.Context, log *slog.Logger, database *db.DB, userID, geminiKey string, dryRun bool, delay time.Duration) (*TagGenResult, error) {
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

		// Generate tags using Gemini, passing existing tags
		generatedTags, err := ai.GenerateTags(ctx, note.Content, existingTagList, geminiKey)
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

		// Add a delay to avoid rate limiting
		if delay > 0 {
			time.Sleep(delay)
		}
	}

	return result, nil
}
