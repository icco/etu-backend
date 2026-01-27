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
	userID := flag.String("user", "", "User ID to sync (required)")
	fullSync := flag.Bool("full", false, "Perform a full sync instead of incremental")
	interval := flag.Duration("interval", 0, "Run continuously with this interval (e.g., 1h). If not set, runs once and exits.")
	flag.Parse()

	if *userID == "" {
		log.Fatal("Error: -user flag is required")
	}

	log.Printf("Starting Notion sync job")
	log.Printf("  User ID: %s", *userID)
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

	// Initialize Notion client
	notionClient, err := notion.NewClient()
	if err != nil {
		log.Fatalf("Failed to initialize Notion client: %v", err)
	}
	log.Println("Notion client initialized")

	// Create syncer
	syncer := sync.NewSyncer(database, notionClient)

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
		runContinuously(ctx, syncer, *userID, *fullSync, *interval)
	} else {
		// Run once
		runOnce(ctx, syncer, *userID, *fullSync)
	}
}

func runOnce(ctx context.Context, syncer *sync.Syncer, userID string, fullSync bool) {
	result, err := syncer.SyncUser(ctx, userID, fullSync)
	if err != nil {
		log.Fatalf("Sync failed: %v", err)
	}

	log.Printf("Sync completed in %s", result.Duration)
	log.Printf("  Created: %d", result.Created)
	log.Printf("  Updated: %d", result.Updated)
	log.Printf("  Unchanged: %d", result.Unchanged)
	log.Printf("  Errors: %d", result.Errors)
}

func runContinuously(ctx context.Context, syncer *sync.Syncer, userID string, fullSync bool, interval time.Duration) {
	// Run immediately on start
	performSync(ctx, syncer, userID, fullSync)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down sync job")
			return
		case <-ticker.C:
			// After the first run, always do incremental syncs unless --full was specified
			performSync(ctx, syncer, userID, fullSync)
		}
	}
}

func performSync(ctx context.Context, syncer *sync.Syncer, userID string, fullSync bool) {
	log.Printf("Starting sync at %s", time.Now().Format(time.RFC3339))

	result, err := syncer.SyncUser(ctx, userID, fullSync)
	if err != nil {
		log.Printf("Sync failed: %v", err)
		return
	}

	log.Printf("Sync completed in %s: created=%d updated=%d unchanged=%d errors=%d",
		result.Duration, result.Created, result.Updated, result.Unchanged, result.Errors)
}
