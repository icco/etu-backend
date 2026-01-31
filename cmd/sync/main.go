package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/icco/etu-backend/internal/notion"
	"github.com/icco/etu-backend/internal/sync"
	"github.com/icco/etu-backend/internal/syncdb"
)

func main() {
	// Parse command line flags
	fullSync := flag.Bool("full", false, "Perform a full sync instead of incremental")
	direction := flag.String("direction", "from-notion", "Sync direction: from-notion, to-notion, or bidirectional")
	interval := flag.Duration("interval", 0, "Run continuously with this interval (e.g., 1h). If not set, runs once and exits.")
	flag.Parse()

	// Validate direction flag
	validDirections := map[string]bool{
		"from-notion":   true,
		"to-notion":     true,
		"bidirectional": true,
	}
	if !validDirections[*direction] {
		log.Fatalf("Error: invalid -direction value %q. Must be one of: from-notion, to-notion, bidirectional", *direction)
	}

	log.Printf("Starting Notion sync job for all users with Notion keys")
	log.Printf("  Direction: %s", *direction)
	log.Printf("  Full sync: %v", *fullSync)
	if *interval > 0 {
		log.Printf("  Interval: %s", *interval)
	}

	// Initialize database with GORM
	database, err := syncdb.New()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Printf("Error closing database: %v", err)
		}
	}()
	log.Println("Connected to database")

	// Run auto-migrations to ensure all tables exist
	if err := database.AutoMigrate(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Handle graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Received shutdown signal, stopping...")
		cancel()
	}()

	if *interval > 0 {
		// Run continuously
		runContinuously(ctx, database, *fullSync, *direction, *interval)
	} else {
		// Run once
		runOnce(ctx, database, *fullSync, *direction)
	}
}

func runOnce(ctx context.Context, database *syncdb.DB, fullSync bool, syncMode string) {
	syncAllUsers(ctx, database, fullSync, syncMode)
}

func runContinuously(ctx context.Context, database *syncdb.DB, fullSync bool, syncMode string, interval time.Duration) {
	// Run immediately on start
	syncAllUsers(ctx, database, fullSync, syncMode)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down sync job")
			return
		case <-ticker.C:
			// After the first run, always do incremental syncs unless --full was specified
			syncAllUsers(ctx, database, fullSync, syncMode)
		}
	}
}

func syncAllUsers(ctx context.Context, database *syncdb.DB, fullSync bool, syncMode string) {
	log.Printf("Starting sync for all users at %s", time.Now().Format(time.RFC3339))

	// Get all users with Notion keys
	users, err := database.GetUsersWithNotionKeys(ctx)
	if err != nil {
		log.Printf("Error: Failed to get users with Notion keys: %v", err)
		return
	}

	if len(users) == 0 {
		log.Println("No users with Notion API keys configured")
		return
	}

	log.Printf("Found %d user(s) with Notion keys configured", len(users))

	successCount := 0
	failureCount := 0

	for _, user := range users {
		if user.NotionKey == nil || *user.NotionKey == "" {
			continue
		}

		log.Printf("Syncing user %s...", user.ID)

		// Create Notion client with user's API key
		notionClient := notion.NewClientWithKey(*user.NotionKey)
		syncer := sync.NewSyncer(database, notionClient)

		// Try to sync and track success/failure
		syncResult := performSyncWithResult(ctx, syncer, user.ID, fullSync, syncMode)
		if syncResult {
			successCount++
		} else {
			failureCount++
		}
	}

	log.Printf("Completed sync for all users: %d succeeded, %d failed", successCount, failureCount)
}

func performSyncWithResult(ctx context.Context, syncer *sync.Syncer, userID string, fullSync bool, syncMode string) bool {
	switch syncMode {
	case "to-notion":
		result, err := syncer.SyncUserToNotion(ctx, userID)
		if err != nil {
			log.Printf(`{"user": "%s", "direction": "to-notion", "status": "failed", "error": "%v"}`, userID, err)
			return false
		}
		log.Printf(`{"user": "%s", "direction": "to-notion", "status": "success", "duration": "%s", "created": %d, "updated": %d, "archived": %d, "errors": %d}`,
			userID, result.Duration, result.Created, result.Updated, result.Archived, result.Errors)
		return result.Errors == 0

	case "bidirectional":
		fromResult, toResult, err := syncer.SyncUserBidirectional(ctx, userID, fullSync)
		if err != nil {
			log.Printf(`{"user": "%s", "direction": "bidirectional", "status": "failed", "error": "%v"}`, userID, err)
			return false
		}
		log.Printf(`{"user": "%s", "direction": "bidirectional", "status": "success", "from_notion": {"duration": "%s", "created": %d, "updated": %d, "unchanged": %d, "errors": %d}, "to_notion": {"duration": "%s", "created": %d, "updated": %d, "archived": %d, "errors": %d}}`,
			userID,
			fromResult.Duration, fromResult.Created, fromResult.Updated, fromResult.Unchanged, fromResult.Errors,
			toResult.Duration, toResult.Created, toResult.Updated, toResult.Archived, toResult.Errors)
		return fromResult.Errors == 0 && toResult.Errors == 0

	default: // from-notion
		result, err := syncer.SyncUser(ctx, userID, fullSync)
		if err != nil {
			log.Printf(`{"user": "%s", "direction": "from-notion", "status": "failed", "error": "%v"}`, userID, err)
			return false
		}
		log.Printf(`{"user": "%s", "direction": "from-notion", "status": "success", "duration": "%s", "created": %d, "updated": %d, "unchanged": %d, "errors": %d}`,
			userID, result.Duration, result.Created, result.Updated, result.Unchanged, result.Errors)
		return result.Errors == 0
	}
}
