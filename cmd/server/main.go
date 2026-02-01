package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/icco/etu-backend/internal/auth"
	"github.com/icco/etu-backend/internal/db"
	"github.com/icco/etu-backend/internal/service"
	"github.com/icco/etu-backend/internal/storage"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

var (
	CommitSHA = "unknown"
)

func main() {
	grpcPort := os.Getenv("GRPC_PORT")
	if grpcPort == "" {
		grpcPort = "50051"
	}

	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "8080"
	}

	log.Printf("Starting etu-backend server (commit: %s)", CommitSHA)

	// Initialize database
	database, err := db.New()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Printf("Error closing database: %v", err)
		}
	}()
	log.Println("Connected to database")

	// Run database migrations
	if err := database.AutoMigrate(); err != nil {
		log.Fatalf("Failed to run database migrations: %v", err)
	}
	log.Println("Database migrations completed")

	// Initialize authenticator
	authenticator, err := auth.New()
	if err != nil {
		log.Fatalf("Failed to initialize authenticator: %v", err)
	}
	defer func() {
		if err := authenticator.Close(); err != nil {
			log.Printf("Error closing authenticator: %v", err)
		}
	}()
	log.Println("Authenticator initialized")

	// Initialize GCS storage client (optional - image uploads won't work without it)
	var storageClient *storage.Client
	gcsBucket := os.Getenv("GCS_BUCKET")
	if gcsBucket != "" {
		ctx := context.Background()
		storageClient, err = storage.New(ctx, gcsBucket)
		if err != nil {
			log.Printf("Warning: Failed to initialize GCS storage client: %v", err)
			log.Println("Image uploads will be disabled")
		} else {
			defer func() {
				if err := storageClient.Close(); err != nil {
					log.Printf("Error closing storage client: %v", err)
				}
			}()
			log.Printf("GCS storage initialized with bucket: %s", gcsBucket)
		}
	} else {
		log.Println("GCS_BUCKET not set - image uploads will be disabled")
	}

	// Get Gemini API key for OCR (optional)
	geminiAPIKey := os.Getenv("GEMINI_API_KEY")
	if geminiAPIKey != "" {
		log.Println("Gemini API key configured for image OCR")
	} else {
		log.Println("GEMINI_API_KEY not set - image OCR will be disabled")
	}

	// Get imgix domain for image URLs (optional)
	imgixDomain := os.Getenv("IMGIX_DOMAIN")
	if imgixDomain != "" {
		log.Printf("Imgix configured with domain: %s", imgixDomain)
	} else {
		log.Println("IMGIX_DOMAIN not set - using GCS signed URLs for images")
	}

	// Create gRPC server with authentication interceptor
	server := grpc.NewServer(
		grpc.UnaryInterceptor(authInterceptor(authenticator)),
	)

	// Register services
	notesService := service.NewNotesService(database, storageClient, geminiAPIKey, imgixDomain)
	tagsService := service.NewTagsService(database)
	authService := service.NewAuthService(database)
	apiKeysService := service.NewApiKeysService(database)
	userSettingsService := service.NewUserSettingsService(database)

	pb.RegisterNotesServiceServer(server, notesService)
	pb.RegisterTagsServiceServer(server, tagsService)
	pb.RegisterAuthServiceServer(server, authService)
	pb.RegisterApiKeysServiceServer(server, apiKeysService)
	pb.RegisterUserSettingsServiceServer(server, userSettingsService)

	// Enable reflection for development/debugging
	reflection.Register(server)

	// Start gRPC listener
	grpcListener, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		log.Fatalf("Failed to listen on gRPC port %s: %v", grpcPort, err)
	}

	// Create HTTP server for health checks
	httpServer := &http.Server{
		Addr:         ":" + httpPort,
		Handler:      newHealthHandler(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	// Start HTTP server in goroutine
	go func() {
		log.Printf("HTTP health server listening on :%s", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to serve HTTP: %v", err)
		}
	}()

	// Start gRPC server in goroutine
	go func() {
		log.Printf("gRPC server listening on :%s", grpcPort)
		if err := server.Serve(grpcListener); err != nil {
			log.Fatalf("Failed to serve gRPC: %v", err)
		}
	}()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down servers...")

	// Shutdown HTTP server with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Gracefully stop gRPC server
	server.GracefulStop()

	log.Println("Servers stopped")
}

// newHealthHandler creates an HTTP handler for health check endpoints
func newHealthHandler() http.Handler {
	mux := http.NewServeMux()

	// Root health check
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"commit": CommitSHA,
		}); err != nil {
			log.Printf("Error encoding health response: %v", err)
		}
	})

	// Explicit health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"commit": CommitSHA,
		}); err != nil {
			log.Printf("Error encoding health response: %v", err)
		}
	})

	// Readiness check (could add DB checks here if needed)
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status": "ready",
		}); err != nil {
			log.Printf("Error encoding ready response: %v", err)
		}
	})

	return mux
}

// authInterceptor creates a gRPC interceptor that validates API keys and M2M tokens
func authInterceptor(authenticator *auth.Authenticator) grpc.UnaryServerInterceptor {
	// Methods that don't require authentication
	publicMethods := map[string]bool{
		"/etu.AuthService/Register":        true,
		"/etu.AuthService/Authenticate":    true,
		"/etu.ApiKeysService/VerifyApiKey": true,
	}

	// Load GRPC API key from environment for server-to-server auth
	grpcApiKey := os.Getenv("GRPC_API_KEY")
	if grpcApiKey != "" {
		log.Println("GRPC API key authentication enabled")
	}

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Skip auth for public methods
		if publicMethods[info.FullMethod] {
			log.Printf("Public request: method=%s", info.FullMethod)
			return handler(ctx, req)
		}

		// Extract metadata from context
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		// Get authorization header
		authHeaders := md.Get("authorization")
		if len(authHeaders) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization header")
		}

		token := authHeaders[0]

		// Check for GRPC API key (server-to-server auth)
		if grpcApiKey != "" && token == grpcApiKey {
			// GRPC API key authentication successful - no user context
			ctx = auth.SetAuthContext(ctx, "m2m", "m2m")
			log.Printf("GRPC API key authenticated request: method=%s", info.FullMethod)
			return handler(ctx, req)
		}

		// Fall back to API key verification
		userID, err := authenticator.VerifyAPIKey(ctx, token)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "invalid API key: %v", err)
		}

		// Add user ID to context for use by handlers
		ctx = auth.SetAuthContext(ctx, userID, "apikey")

		// Log the authenticated request
		log.Printf("Authenticated request: method=%s user=%s", info.FullMethod, userID)

		return handler(ctx, req)
	}
}
