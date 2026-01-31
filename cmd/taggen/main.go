package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/icco/etu-backend/internal/ai"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func main() {
	// Parse command line flags
	interval := flag.Duration("interval", 0, "Run continuously with this interval (e.g., 1h). If not set, runs once and exits.")
	dryRun := flag.Bool("dry-run", false, "Run without actually adding tags (for testing)")
	delay := flag.Duration("delay", 2*time.Second, "Delay between processing notes to avoid rate limiting (e.g., 2s)")
	flag.Parse()

	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" {
		log.Fatal("Error: GEMINI_API_KEY environment variable not set")
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

	log.Printf("Starting Gemini tag generation job for all users")
	log.Printf("  Dry run: %v", *dryRun)
	log.Printf("  Delay: %s", *delay)
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
	notesClient := pb.NewNotesServiceClient(conn)
	tagsClient := pb.NewTagsServiceClient(conn)

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
		runContinuously(ctx, authClient, notesClient, tagsClient, geminiKey, *dryRun, *delay, *interval)
	} else {
		// Run once
		runOnce(ctx, authClient, notesClient, tagsClient, geminiKey, *dryRun, *delay)
	}
}

func runOnce(ctx context.Context, authClient pb.AuthServiceClient, notesClient pb.NotesServiceClient, tagsClient pb.TagsServiceClient, geminiKey string, dryRun bool, delay time.Duration) {
	result, err := generateTagsForAllUsers(ctx, authClient, notesClient, tagsClient, geminiKey, dryRun, delay)
	if err != nil {
		log.Fatalf("Tag generation failed: %v", err)
	}

	log.Printf("Tag generation completed in %s", result.Duration)
	log.Printf("  Users processed: %d", result.UsersProcessed)
	log.Printf("  Notes processed: %d", result.NotesProcessed)
	log.Printf("  Tags added: %d", result.TagsAdded)
	log.Printf("  Errors: %d", result.Errors)
}

func runContinuously(ctx context.Context, authClient pb.AuthServiceClient, notesClient pb.NotesServiceClient, tagsClient pb.TagsServiceClient, geminiKey string, dryRun bool, delay time.Duration, interval time.Duration) {
	// Run immediately on start
	performTagGeneration(ctx, authClient, notesClient, tagsClient, geminiKey, dryRun, delay)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down tag generation job")
			return
		case <-ticker.C:
			performTagGeneration(ctx, authClient, notesClient, tagsClient, geminiKey, dryRun, delay)
		}
	}
}

func performTagGeneration(ctx context.Context, authClient pb.AuthServiceClient, notesClient pb.NotesServiceClient, tagsClient pb.TagsServiceClient, geminiKey string, dryRun bool, delay time.Duration) {
	log.Printf("Starting tag generation at %s", time.Now().Format(time.RFC3339))

	result, err := generateTagsForAllUsers(ctx, authClient, notesClient, tagsClient, geminiKey, dryRun, delay)
	if err != nil {
		log.Printf("Tag generation failed: %v", err)
		return
	}

	log.Printf("Tag generation completed in %s: users=%d notes=%d tags=%d errors=%d",
		result.Duration, result.UsersProcessed, result.NotesProcessed, result.TagsAdded, result.Errors)
}

// generateTagsForAllUsers generates tags for all users in the database
func generateTagsForAllUsers(ctx context.Context, authClient pb.AuthServiceClient, notesClient pb.NotesServiceClient, tagsClient pb.TagsServiceClient, geminiKey string, dryRun bool, delay time.Duration) (*TagGenResult, error) {
	start := time.Now()
	result := &TagGenResult{}

	// Get all users via gRPC
	usersResp, err := authClient.ListAllUsers(ctx, &pb.ListAllUsersRequest{})
	if err != nil {
		return nil, err
	}

	log.Printf("Found %d users to process", len(usersResp.Users))

	for _, user := range usersResp.Users {
		select {
		case <-ctx.Done():
			result.Duration = time.Since(start)
			return result, ctx.Err()
		default:
		}

		log.Printf("Processing user %s", user.Id)
		userResult, err := generateTagsForUser(ctx, notesClient, tagsClient, user.Id, geminiKey, dryRun, delay)
		if err != nil {
			log.Printf("Failed to generate tags for user %s: %v", user.Id, err)
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

func generateTagsForUser(ctx context.Context, notesClient pb.NotesServiceClient, tagsClient pb.TagsServiceClient, userID, geminiKey string, dryRun bool, delay time.Duration) (*TagGenResult, error) {
	result := &TagGenResult{}

	// Fetch all existing tags for the user to prefer reusing them
	tagsResp, err := tagsClient.ListTags(ctx, &pb.ListTagsRequest{UserId: userID})
	if err != nil {
		return nil, err
	}

	// Create a map of existing tag names (lowercase) for easy lookup
	existingTagNames := make(map[string]bool)
	existingTagList := make([]string, 0, len(tagsResp.Tags))
	for _, tag := range tagsResp.Tags {
		lowerName := strings.ToLower(tag.Name)
		existingTagNames[lowerName] = true
		existingTagList = append(existingTagList, lowerName)
	}

	// Fetch notes with less than 3 tags
	notesResp, err := notesClient.GetNotesWithFewTags(ctx, &pb.GetNotesWithFewTagsRequest{
		UserId:  userID,
		MaxTags: 3,
	})
	if err != nil {
		return nil, err
	}

	log.Printf("Found %d notes with fewer than 3 tags", len(notesResp.Notes))

	for _, note := range notesResp.Notes {
		result.NotesProcessed++

		// Calculate how many tags we can add
		currentTagCount := len(note.Tags)
		maxNewTags := 3 - currentTagCount

		if maxNewTags <= 0 {
			continue
		}

		log.Printf("Processing note %s (current tags: %d)", note.Id, currentTagCount)

		// Generate tags using Gemini, passing existing tags
		generatedTags, err := ai.GenerateTags(ctx, note.Content, existingTagList, geminiKey)
		if err != nil {
			log.Printf("Failed to generate tags for note %s: %v", note.Id, err)
			result.Errors++
			continue
		}

		// Filter out tags that already exist on this note
		var newTags []string
		existingNoteTagNames := make(map[string]bool)
		for _, tag := range note.Tags {
			existingNoteTagNames[strings.ToLower(tag)] = true
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
			log.Printf("No new tags to add for note %s", note.Id)
			continue
		}

		log.Printf("Adding %d tags to note %s: %v", len(newTags), note.Id, newTags)

		if !dryRun {
			// Add tags to the note via gRPC
			_, err := notesClient.AddTagsToNote(ctx, &pb.AddTagsToNoteRequest{
				UserId: userID,
				NoteId: note.Id,
				Tags:   newTags,
			})
			if err != nil {
				log.Printf("Failed to add tags to note %s: %v", note.Id, err)
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
