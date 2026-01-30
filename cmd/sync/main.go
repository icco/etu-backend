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
	userID := flag.String("user", "", "User ID to sync (if not specified, syncs all users with Notion keys)")
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

	log.Printf("Starting Notion sync job")
	if *userID != "" {
		log.Printf("  User ID: %s", *userID)
	} else {
		log.Printf("  Mode: All users with Notion keys")
	}
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
	defer database.Close()
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
		runContinuously(ctx, database, *userID, *fullSync, *direction, *interval)
	} else {
		// Run once
		runOnce(ctx, database, *userID, *fullSync, *direction)
	}
}

func runOnce(ctx context.Context, database *syncdb.DB, userID string, fullSync bool, syncMode string) {
	if userID != "" {
		// Sync single user
		syncSingleUser(ctx, database, userID, fullSync, syncMode)
	} else {
		// Sync all users with Notion keys
		syncAllUsers(ctx, database, fullSync, syncMode)
	}
}

func runContinuously(ctx context.Context, database *syncdb.DB, userID string, fullSync bool, syncMode string, interval time.Duration) {
	// Run immediately on start
	if userID != "" {
		syncSingleUser(ctx, database, userID, fullSync, syncMode)
	} else {
		syncAllUsers(ctx, database, fullSync, syncMode)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down sync job")
			return
		case <-ticker.C:
			// After the first run, always do incremental syncs unless --full was specified
			if userID != "" {
				syncSingleUser(ctx, database, userID, fullSync, syncMode)
			} else {
				syncAllUsers(ctx, database, fullSync, syncMode)
			}
		}
	}
}

func syncSingleUser(ctx context.Context, database *syncdb.DB, userID string, fullSync bool, syncMode string) {
	log.Printf("Starting sync for user %s at %s", userID, time.Now().Format(time.RFC3339))

	// Get user settings to retrieve Notion API key
	settings, err := database.GetUserSettings(ctx, userID)
	if err != nil {
		log.Printf("Error: Failed to get user settings for user %s: %v", userID, err)
		return
	}

	if settings == nil || settings.NotionKey == nil || *settings.NotionKey == "" {
		log.Printf("Error: No Notion API key configured for user %s", userID)
		return
	}

	// Create Notion client with user's API key
	notionClient := notion.NewClientWithKey(*settings.NotionKey)
	syncer := sync.NewSyncer(database, notionClient)

	performSync(ctx, syncer, userID, fullSync, syncMode)
}

func syncAllUsers(ctx context.Context, database *syncdb.DB, fullSync bool, syncMode string) {
	log.Printf("Starting sync for all users at %s", time.Now().Format(time.RFC3339))

	// Get all users with Notion keys
	usersWithKeys, err := database.GetUsersWithNotionKeys(ctx)
	if err != nil {
		log.Printf("Error: Failed to get users with Notion keys: %v", err)
		return
	}

	if len(usersWithKeys) == 0 {
		log.Println("No users with Notion API keys configured")
		return
	}

	log.Printf("Found %d user(s) with Notion keys configured", len(usersWithKeys))

	successCount := 0
	failureCount := 0

	for _, settings := range usersWithKeys {
		if settings.NotionKey == nil || *settings.NotionKey == "" {
			continue
		}

		log.Printf("Syncing user %s...", settings.UserID)

		// Create Notion client with user's API key
		notionClient := notion.NewClientWithKey(*settings.NotionKey)
		syncer := sync.NewSyncer(database, notionClient)

		// Try to sync and track success/failure
		syncResult := performSyncWithResult(ctx, syncer, settings.UserID, fullSync, syncMode)
		if syncResult {
			successCount++
		} else {
			failureCount++
		}
	}

	log.Printf("Completed sync for all users: %d succeeded, %d failed", successCount, failureCount)
}

func performSync(ctx context.Context, syncer *sync.Syncer, userID string, fullSync bool, syncMode string) {
	performSyncWithResult(ctx, syncer, userID, fullSync, syncMode)
}

func performSyncWithResult(ctx context.Context, syncer *sync.Syncer, userID string, fullSync bool, syncMode string) bool {
	switch syncMode {
	case "to-notion":
		result, err := syncer.SyncUserToNotion(ctx, userID)
		if err != nil {
			log.Printf("Sync to Notion failed for user %s: %v", userID, err)
			return false
		}
		log.Printf("User %s - Sync to Notion completed in %s", userID, result.Duration)
		log.Printf("  Created: %d, Updated: %d, Archived: %d, Errors: %d",
			result.Created, result.Updated, result.Archived, result.Errors)
		return result.Errors == 0

	case "bidirectional":
		fromResult, toResult, err := syncer.SyncUserBidirectional(ctx, userID, fullSync)
		if err != nil {
			log.Printf("Bidirectional sync failed for user %s: %v", userID, err)
			return false
		}
		log.Printf("User %s - Bidirectional sync completed", userID)
		log.Printf("  From Notion (in %s): Created: %d, Updated: %d, Unchanged: %d, Errors: %d",
			fromResult.Duration, fromResult.Created, fromResult.Updated, fromResult.Unchanged, fromResult.Errors)
		log.Printf("  To Notion (in %s): Created: %d, Updated: %d, Archived: %d, Errors: %d",
			toResult.Duration, toResult.Created, toResult.Updated, toResult.Archived, toResult.Errors)
		return fromResult.Errors == 0 && toResult.Errors == 0

	default: // from-notion
		result, err := syncer.SyncUser(ctx, userID, fullSync)
		if err != nil {
			log.Printf("Sync failed for user %s: %v", userID, err)
			return false
		}
		log.Printf("User %s - Sync completed in %s", userID, result.Duration)
		log.Printf("  Created: %d, Updated: %d, Unchanged: %d, Errors: %d",
			result.Created, result.Updated, result.Unchanged, result.Errors)
		return result.Errors == 0
	}
}
