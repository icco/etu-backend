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
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
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

	// Get gRPC server address
	grpcAddr := os.Getenv("GRPC_SERVER_ADDR")
	if grpcAddr == "" {
		grpcAddr = "localhost:50051"
	}

	// Get API key for authentication
	grpcApiKey := os.Getenv("GRPC_API_KEY")
	if grpcApiKey == "" {
		log.Fatal("Error: GRPC_API_KEY environment variable not set")
	}

	log.Printf("Starting Notion sync job for all users with Notion keys")
	log.Printf("  Direction: %s", *direction)
	log.Printf("  Full sync: %v", *fullSync)
	log.Printf("  Server: %s", grpcAddr)
	if *interval > 0 {
		log.Printf("  Interval: %s", *interval)
	}

	// Connect to gRPC server
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to gRPC server: %v", err)
	}
	defer conn.Close()
	log.Println("Connected to gRPC server")

	// Create gRPC clients
	authClient := pb.NewAuthServiceClient(conn)
	syncClient := pb.NewSyncServiceClient(conn)
	notesClient := pb.NewNotesServiceClient(conn)
	userSettingsClient := pb.NewUserSettingsServiceClient(conn)

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

	// Add API key to context for all requests
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", grpcApiKey)

	if *interval > 0 {
		// Run continuously
		runContinuously(ctx, authClient, syncClient, notesClient, userSettingsClient, *fullSync, *direction, *interval)
	} else {
		// Run once
		runOnce(ctx, authClient, syncClient, notesClient, userSettingsClient, *fullSync, *direction)
	}
}

func runOnce(ctx context.Context, authClient pb.AuthServiceClient, syncClient pb.SyncServiceClient, notesClient pb.NotesServiceClient, userSettingsClient pb.UserSettingsServiceClient, fullSync bool, syncMode string) {
	syncAllUsers(ctx, authClient, syncClient, notesClient, userSettingsClient, fullSync, syncMode)
}

func runContinuously(ctx context.Context, authClient pb.AuthServiceClient, syncClient pb.SyncServiceClient, notesClient pb.NotesServiceClient, userSettingsClient pb.UserSettingsServiceClient, fullSync bool, syncMode string, interval time.Duration) {
	// Run immediately on start
	syncAllUsers(ctx, authClient, syncClient, notesClient, userSettingsClient, fullSync, syncMode)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down sync job")
			return
		case <-ticker.C:
			// After the first run, always do incremental syncs unless --full was specified
			syncAllUsers(ctx, authClient, syncClient, notesClient, userSettingsClient, fullSync, syncMode)
		}
	}
}

func syncAllUsers(ctx context.Context, authClient pb.AuthServiceClient, syncClient pb.SyncServiceClient, notesClient pb.NotesServiceClient, userSettingsClient pb.UserSettingsServiceClient, fullSync bool, syncMode string) {
	log.Printf("Starting sync for all users at %s", time.Now().Format(time.RFC3339))

	// Get all users with Notion keys via gRPC
	usersResp, err := authClient.ListUsersWithNotionKeys(ctx, &pb.ListUsersWithNotionKeysRequest{})
	if err != nil {
		log.Printf("Error: Failed to get users with Notion keys: %v", err)
		return
	}

	if len(usersResp.Users) == 0 {
		log.Println("No users with Notion API keys configured")
		return
	}

	log.Printf("Found %d user(s) with Notion keys configured", len(usersResp.Users))

	successCount := 0
	failureCount := 0

	for _, user := range usersResp.Users {
		// Get user settings to get the Notion key
		settingsResp, err := userSettingsClient.GetUserSettings(ctx, &pb.GetUserSettingsRequest{UserId: user.Id})
		if err != nil {
			log.Printf("Error getting settings for user %s: %v", user.Id, err)
			failureCount++
			continue
		}

		if settingsResp.Settings.NotionKey == nil || *settingsResp.Settings.NotionKey == "" {
			continue
		}

		log.Printf("Syncing user %s...", user.Id)

		// Create Notion client with user's API key
		notionClient := notion.NewClientWithKey(*settingsResp.Settings.NotionKey)
		syncer := sync.NewSyncer(syncClient, notesClient, notionClient)

		// Try to sync and track success/failure
		syncResult := performSyncWithResult(ctx, syncer, user.Id, fullSync, syncMode)
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
