package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/icco/etu-backend/internal/auth"
	"github.com/icco/etu-backend/internal/db"
	"github.com/icco/etu-backend/internal/logger"
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
	log := logger.New()

	grpcPort := os.Getenv("GRPC_PORT")
	if grpcPort == "" {
		grpcPort = "50051"
	}

	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "8080"
	}

	log.Info("starting etu-backend server", "commit", CommitSHA, "grpc_port", grpcPort, "http_port", httpPort)

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

	// Run database migrations
	if err := database.AutoMigrate(); err != nil {
		log.Error("failed to run database migrations", "error", err)
		os.Exit(1)
	}
	log.Info("database initialized and migrations completed")

	// Initialize authenticator
	authenticator, err := auth.New()
	if err != nil {
		log.Error("failed to initialize authenticator", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := authenticator.Close(); err != nil {
			log.Error("error closing authenticator", "error", err)
		}
	}()
	log.Info("authenticator initialized")

	// Initialize GCS storage client (optional - image uploads won't work without it)
	var storageClient *storage.Client
	gcsBucket := os.Getenv("GCS_BUCKET")
	if gcsBucket != "" {
		ctx := context.Background()
		storageClient, err = storage.New(ctx, gcsBucket)
		if err != nil {
			log.Warn("failed to initialize GCS storage client, image uploads will be disabled", "error", err, "bucket", gcsBucket)
		} else {
			defer func() {
				if err := storageClient.Close(); err != nil {
					log.Error("error closing storage client", "error", err)
				}
			}()
			log.Info("GCS storage initialized", "bucket", gcsBucket)
		}
	} else {
		log.Info("GCS storage not configured, image uploads will be disabled")
	}

	// Get Gemini API key for OCR (optional)
	geminiAPIKey := os.Getenv("GEMINI_API_KEY")
	geminiEnabled := geminiAPIKey != ""

	// Get imgix domain for image URLs (optional)
	imgixDomain := os.Getenv("IMGIX_DOMAIN")

	log.Info("optional features configured",
		"gemini_ocr_enabled", geminiEnabled,
		"imgix_enabled", imgixDomain != "",
		"imgix_domain", imgixDomain)

	// Create gRPC server with authentication interceptor
	server := grpc.NewServer(
		grpc.UnaryInterceptor(authInterceptor(authenticator, log)),
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
		log.Error("failed to listen on gRPC port", "port", grpcPort, "error", err)
		os.Exit(1)
	}

	// Create HTTP server for health checks
	httpServer := &http.Server{
		Addr:         ":" + httpPort,
		Handler:      newHealthHandler(log),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	// Start HTTP server in goroutine
	go func() {
		log.Info("HTTP health server listening", "port", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// Start gRPC server in goroutine
	go func() {
		log.Info("gRPC server listening", "port", grpcPort)
		if err := server.Serve(grpcListener); err != nil {
			log.Error("gRPC server error", "error", err)
			os.Exit(1)
		}
	}()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	log.Info("shutting down servers", "signal", sig.String())

	// Shutdown HTTP server with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Error("HTTP server shutdown error", "error", err)
	}

	// Gracefully stop gRPC server
	server.GracefulStop()

	log.Info("servers stopped gracefully")
}

// newHealthHandler creates an HTTP handler for health check endpoints
func newHealthHandler(log *slog.Logger) http.Handler {
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
			log.Error("error encoding health response", "error", err)
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
			log.Error("error encoding health response", "error", err)
		}
	})

	// Readiness check (could add DB checks here if needed)
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status": "ready",
		}); err != nil {
			log.Error("error encoding ready response", "error", err)
		}
	})

	return mux
}

// authInterceptor creates a gRPC interceptor that validates API keys and M2M tokens
func authInterceptor(authenticator *auth.Authenticator, log *slog.Logger) grpc.UnaryServerInterceptor {
	// Methods that don't require authentication
	publicMethods := map[string]bool{
		"/etu.AuthService/Register":        true,
		"/etu.AuthService/Authenticate":    true,
		"/etu.ApiKeysService/VerifyApiKey": true,
	}

	// Load GRPC API key from environment for server-to-server auth
	grpcApiKey := os.Getenv("GRPC_API_KEY")
	grpcAuthEnabled := grpcApiKey != ""

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Skip auth for public methods
		if publicMethods[info.FullMethod] {
			log.Info("public request", "method", info.FullMethod)
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
		if grpcAuthEnabled && token == grpcApiKey {
			// GRPC API key authentication successful - no user context
			ctx = auth.SetAuthContext(ctx, "m2m", "m2m")
			log.Info("authenticated request", "method", info.FullMethod, "auth_type", "m2m")
			return handler(ctx, req)
		}

		// Fall back to API key verification
		userID, err := authenticator.VerifyAPIKey(ctx, token)
		if err != nil {
			log.Warn("authentication failed", "method", info.FullMethod, "error", err.Error())
			return nil, status.Errorf(codes.Unauthenticated, "invalid API key: %v", err)
		}

		// Add user ID to context for use by handlers
		ctx = auth.SetAuthContext(ctx, userID, "apikey")

		// Log the authenticated request
		log.Info("authenticated request", "method", info.FullMethod, "user_id", userID, "auth_type", "apikey")

		return handler(ctx, req)
	}
}
