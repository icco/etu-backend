package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/icco/etu-backend/internal/logger"
	"github.com/icco/etu-backend/internal/notion"
	"github.com/icco/etu-backend/internal/sync"
	"github.com/icco/etu-backend/internal/syncdb"
)

func main() {
	log := logger.New()

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
		log.Error("invalid direction value", "direction", *direction, "valid_options", []string{"from-notion", "to-notion", "bidirectional"})
		os.Exit(1)
	}

	intervalStr := "once"
	if *interval > 0 {
		intervalStr = interval.String()
	}

	log.Info("starting Notion sync job", 
		"direction", *direction,
		"full_sync", *fullSync,
		"continuous", *interval > 0,
		"interval", intervalStr)

	// Initialize database with GORM
	database, err := syncdb.New()
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Error("error closing database", "error", err)
		}
	}()

	// Run auto-migrations to ensure all tables exist
	if err := database.AutoMigrate(); err != nil {
		log.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}
	log.Info("database connected and migrations completed")

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
		runContinuously(ctx, log, database, *fullSync, *direction, *interval)
	} else {
		// Run once
		runOnce(ctx, log, database, *fullSync, *direction)
	}
}

func runOnce(ctx context.Context, log *slog.Logger, database *syncdb.DB, fullSync bool, syncMode string) {
	syncAllUsers(ctx, log, database, fullSync, syncMode)
}

func runContinuously(ctx context.Context, log *slog.Logger, database *syncdb.DB, fullSync bool, syncMode string, interval time.Duration) {
	// Run immediately on start
	syncAllUsers(ctx, log, database, fullSync, syncMode)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("shutting down sync job")
			return
		case <-ticker.C:
			// After the first run, always do incremental syncs unless --full was specified
			syncAllUsers(ctx, log, database, fullSync, syncMode)
		}
	}
}

func syncAllUsers(ctx context.Context, log *slog.Logger, database *syncdb.DB, fullSync bool, syncMode string) {
	log.Info("starting sync for all users", "timestamp", time.Now().Format(time.RFC3339))

	// Get all users with Notion keys
	users, err := database.GetUsersWithNotionKeys(ctx)
	if err != nil {
		log.Error("failed to get users with Notion keys", "error", err)
		return
	}

	if len(users) == 0 {
		log.Info("no users with Notion API keys configured")
		return
	}

	log.Info("found users with Notion keys", "count", len(users))

	successCount := 0
	failureCount := 0

	for _, user := range users {
		if user.NotionKey == nil || *user.NotionKey == "" {
			continue
		}

		// Create Notion client with user's API key
		notionClient := notion.NewClientWithKey(*user.NotionKey)
		syncer := sync.NewSyncer(database, notionClient)

		// Try to sync and track success/failure
		syncResult := performSyncWithResult(ctx, log, syncer, user.ID, fullSync, syncMode)
		if syncResult {
			successCount++
		} else {
			failureCount++
		}
	}

	log.Info("completed sync for all users", 
		"succeeded", successCount,
		"failed", failureCount,
		"total", len(users))
}

func performSyncWithResult(ctx context.Context, log *slog.Logger, syncer *sync.Syncer, userID string, fullSync bool, syncMode string) bool {
	switch syncMode {
	case "to-notion":
		result, err := syncer.SyncUserToNotion(ctx, userID)
		if err != nil {
			log.Error("sync to Notion failed", 
				"user_id", userID,
				"direction", "to-notion",
				"error", err)
			return false
		}
		log.Info("sync to Notion completed",
			"user_id", userID,
			"direction", "to-notion",
			"duration", result.Duration.String(),
			"created", result.Created,
			"updated", result.Updated,
			"archived", result.Archived,
			"errors", result.Errors)
		return result.Errors == 0

	case "bidirectional":
		fromResult, toResult, err := syncer.SyncUserBidirectional(ctx, userID, fullSync)
		if err != nil {
			log.Error("bidirectional sync failed",
				"user_id", userID,
				"direction", "bidirectional",
				"error", err)
			return false
		}
		log.Info("bidirectional sync completed",
			"user_id", userID,
			"direction", "bidirectional",
			"from_notion_duration", fromResult.Duration.String(),
			"from_notion_created", fromResult.Created,
			"from_notion_updated", fromResult.Updated,
			"from_notion_unchanged", fromResult.Unchanged,
			"from_notion_errors", fromResult.Errors,
			"to_notion_duration", toResult.Duration.String(),
			"to_notion_created", toResult.Created,
			"to_notion_updated", toResult.Updated,
			"to_notion_archived", toResult.Archived,
			"to_notion_errors", toResult.Errors)
		return fromResult.Errors == 0 && toResult.Errors == 0

	default: // from-notion
		result, err := syncer.SyncUser(ctx, userID, fullSync)
		if err != nil {
			log.Error("sync from Notion failed",
				"user_id", userID,
				"direction", "from-notion",
				"error", err)
			return false
		}
		log.Info("sync from Notion completed",
			"user_id", userID,
			"direction", "from-notion",
			"duration", result.Duration.String(),
			"created", result.Created,
			"updated", result.Updated,
			"unchanged", result.Unchanged,
			"errors", result.Errors)
		return result.Errors == 0
	}
}
