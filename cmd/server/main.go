package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/icco/etu-backend/internal/auth"
	"github.com/icco/etu-backend/internal/db"
	"github.com/icco/etu-backend/internal/service"
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
	port := os.Getenv("PORT")
	if port == "" {
		port = "50051"
	}

	log.Printf("Starting etu-backend gRPC server (commit: %s)", CommitSHA)

	// Initialize database
	database, err := db.New()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()
	log.Println("Connected to database")

	// Initialize authenticator
	authenticator, err := auth.New()
	if err != nil {
		log.Fatalf("Failed to initialize authenticator: %v", err)
	}
	defer authenticator.Close()
	log.Println("Authenticator initialized")

	// Create gRPC server with authentication interceptor
	server := grpc.NewServer(
		grpc.UnaryInterceptor(authInterceptor(authenticator)),
	)

	// Register services
	notesService := service.NewNotesService(database)
	tagsService := service.NewTagsService(database)
	authService := service.NewAuthService(database)
	apiKeysService := service.NewApiKeysService(database)

	pb.RegisterNotesServiceServer(server, notesService)
	pb.RegisterTagsServiceServer(server, tagsService)
	pb.RegisterAuthServiceServer(server, authService)
	pb.RegisterApiKeysServiceServer(server, apiKeysService)

	// Enable reflection for development/debugging
	reflection.Register(server)

	// Start listening
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen on port %s: %v", port, err)
	}

	// Handle graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down gRPC server...")
		server.GracefulStop()
	}()

	log.Printf("gRPC server listening on :%s", port)
	if err := server.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
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
			ctx = context.WithValue(ctx, userIDKey, "m2m")
			ctx = context.WithValue(ctx, authTypeKey, "m2m")
			log.Printf("GRPC API key authenticated request: method=%s", info.FullMethod)
			return handler(ctx, req)
		}

		// Fall back to API key verification
		userID, err := authenticator.VerifyAPIKey(ctx, token)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "invalid API key: %v", err)
		}

		// Add user ID to context for use by handlers
		ctx = context.WithValue(ctx, userIDKey, userID)
		ctx = context.WithValue(ctx, authTypeKey, "apikey")

		// Log the authenticated request
		log.Printf("Authenticated request: method=%s user=%s", info.FullMethod, userID)

		return handler(ctx, req)
	}
}

type contextKey string

const (
	userIDKey   contextKey = "userID"
	authTypeKey contextKey = "authType"
)

// GetUserID extracts the user ID from context
func GetUserID(ctx context.Context) (string, error) {
	userID, ok := ctx.Value(userIDKey).(string)
	if !ok || userID == "" {
		return "", fmt.Errorf("user ID not found in context")
	}
	return userID, nil
}

// GetAuthType extracts the authentication type from context ("m2m" or "apikey")
func GetAuthType(ctx context.Context) string {
	authType, ok := ctx.Value(authTypeKey).(string)
	if !ok {
		return ""
	}
	return authType
}

// IsM2MAuth returns true if the request was authenticated via M2M token
func IsM2MAuth(ctx context.Context) bool {
	return GetAuthType(ctx) == "m2m"
}
