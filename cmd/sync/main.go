package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/icco/etu-backend/internal/logger"
	"github.com/icco/etu-backend/internal/notion"
	"github.com/icco/etu-backend/internal/sync"
	"github.com/icco/etu-backend/internal/syncdb"
	"github.com/icco/gutil/logging"
	"go.uber.org/zap"
)

const (
	dirFromNotion    = "from-notion"
	dirToNotion      = "to-notion"
	dirBidirectional = "bidirectional"
)

func main() {
	log := logger.New("etu-backend-sync")
	if err := run(log); err != nil {
		log.Errorw("sync exited with error", zap.Error(err))
		os.Exit(1)
	}
}

func run(log *zap.SugaredLogger) error {
	rootCtx := logging.NewContext(context.Background(), log)

	fullSync := flag.Bool("full", false, "Perform a full sync instead of incremental")
	direction := flag.String("direction", "from-notion", "Sync direction: from-notion, to-notion, or bidirectional")
	interval := flag.Duration("interval", 0, "Run continuously with this interval (e.g., 1h). If not set, runs once and exits.")
	flag.Parse()

	validDirections := map[string]bool{
		dirFromNotion:    true,
		dirToNotion:      true,
		dirBidirectional: true,
	}
	if !validDirections[*direction] {
		return fmt.Errorf("invalid direction value %q (valid: %s, %s, %s)", *direction, dirFromNotion, dirToNotion, dirBidirectional)
	}

	intervalStr := "once"
	if *interval > 0 {
		intervalStr = interval.String()
	}

	log.Infow("starting Notion sync job",
		"direction", *direction,
		"full_sync", *fullSync,
		"continuous", *interval > 0,
		"interval", intervalStr)

	database, err := syncdb.New()
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Errorw("error closing database", zap.Error(err))
		}
	}()

	if err := database.AutoMigrate(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}
	log.Infow("database connected and migrations completed")

	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Infow("received shutdown signal, stopping", "signal", sig.String())
		cancel()
	}()

	if *interval > 0 {
		runContinuously(ctx, database, *fullSync, *direction, *interval)
	} else {
		runOnce(ctx, database, *fullSync, *direction)
	}
	return nil
}

func runOnce(ctx context.Context, database *syncdb.DB, fullSync bool, syncMode string) {
	syncAllUsers(ctx, database, fullSync, syncMode)
}

func runContinuously(ctx context.Context, database *syncdb.DB, fullSync bool, syncMode string, interval time.Duration) {
	l := logging.FromContext(ctx)

	syncAllUsers(ctx, database, fullSync, syncMode)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.Infow("shutting down sync job")
			return
		case <-ticker.C:
			syncAllUsers(ctx, database, fullSync, syncMode)
		}
	}
}

func syncAllUsers(ctx context.Context, database *syncdb.DB, fullSync bool, syncMode string) {
	l := logging.FromContext(ctx)
	l.Infow("starting sync for all users", "timestamp", time.Now().Format(time.RFC3339))

	users, err := database.GetUsersWithNotionKeys(ctx)
	if err != nil {
		l.Errorw("failed to get users with Notion keys", zap.Error(err))
		return
	}

	if len(users) == 0 {
		l.Infow("no users with Notion API keys configured")
		return
	}

	l.Infow("found users with Notion keys", "count", len(users))

	successCount := 0
	failureCount := 0

	for _, user := range users {
		if user.NotionKey == nil || *user.NotionKey == "" {
			continue
		}

		databaseName := notion.DefaultDatabaseName
		if user.NotionDatabaseName != nil && *user.NotionDatabaseName != "" {
			databaseName = *user.NotionDatabaseName
		}
		notionClient := notion.NewClientWithKey(*user.NotionKey, databaseName)
		syncer := sync.NewSyncer(database, notionClient)

		syncResult := performSyncWithResult(ctx, syncer, user.ID, fullSync, syncMode)
		if syncResult {
			successCount++
		} else {
			failureCount++
		}
	}

	l.Infow("completed sync for all users",
		"succeeded", successCount,
		"failed", failureCount,
		"total", len(users))
}

func performSyncWithResult(ctx context.Context, syncer *sync.Syncer, userID string, fullSync bool, syncMode string) bool {
	l := logging.FromContext(ctx).With("user_id", userID)

	switch syncMode {
	case dirToNotion:
		result, err := syncer.SyncUserToNotion(ctx, userID)
		if err != nil {
			l.Errorw("sync to Notion failed",
				"direction", dirToNotion,
				zap.Error(err))
			return false
		}
		l.Infow("sync to Notion completed",
			"direction", dirToNotion,
			"duration", result.Duration.String(),
			"created", result.Created,
			"updated", result.Updated,
			"archived", result.Archived,
			"errors", result.Errors)
		return result.Errors == 0

	case dirBidirectional:
		fromResult, toResult, err := syncer.SyncUserBidirectional(ctx, userID, fullSync)
		if err != nil {
			l.Errorw("bidirectional sync failed",
				"direction", dirBidirectional,
				zap.Error(err))
			return false
		}
		l.Infow("bidirectional sync completed",
			"direction", dirBidirectional,
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

	default:
		result, err := syncer.SyncUser(ctx, userID, fullSync)
		if err != nil {
			l.Errorw("sync from Notion failed",
				"direction", "from-notion",
				zap.Error(err))
			return false
		}
		l.Infow("sync from Notion completed",
			"direction", "from-notion",
			"duration", result.Duration.String(),
			"created", result.Created,
			"updated", result.Updated,
			"unchanged", result.Unchanged,
			"errors", result.Errors)
		return result.Errors == 0
	}
}
