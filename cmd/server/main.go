package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/icco/etu-backend/internal/ai"
	"github.com/icco/etu-backend/internal/auth"
	"github.com/icco/etu-backend/internal/db"
	"github.com/icco/etu-backend/internal/logger"
	"github.com/icco/etu-backend/internal/service"
	"github.com/icco/etu-backend/internal/storage"
	pb "github.com/icco/etu-backend/proto"
	"github.com/icco/gutil/logging"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.uber.org/zap"
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
	log := logger.New("etu-backend-server")

	// rootCtx carries the application logger so any code path that derives a
	// context (HTTP handlers, gRPC interceptors, background goroutines) can
	// retrieve a logger via logging.FromContext.
	rootCtx := logging.NewContext(context.Background(), log)

	grpcPort := os.Getenv("GRPC_PORT")
	if grpcPort == "" {
		grpcPort = "50051"
	}

	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "8080"
	}

	log.Infow("starting etu-backend server", "commit", CommitSHA, "grpc_port", grpcPort, "http_port", httpPort)

	// OpenTelemetry meter provider exporting to Prometheus. Only HTTP is
	// instrumented today; the /metrics endpoint is mounted on the health
	// HTTP server below.
	registry := prometheus.NewRegistry()
	exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		log.Errorw("failed to init prometheus exporter", zap.Error(err))
		os.Exit(1)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	otel.SetMeterProvider(mp)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := mp.Shutdown(shutdownCtx); err != nil {
			log.Warnw("meter provider shutdown", zap.Error(err))
		}
	}()

	database, err := db.New()
	if err != nil {
		log.Errorw("failed to connect to database", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Errorw("error closing database", zap.Error(err))
		}
	}()

	if err := database.AutoMigrate(); err != nil {
		log.Errorw("failed to run database migrations", zap.Error(err))
		os.Exit(1)
	}
	log.Infow("database initialized and migrations completed")

	authenticator, err := auth.New()
	if err != nil {
		log.Errorw("failed to initialize authenticator", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		if err := authenticator.Close(); err != nil {
			log.Errorw("error closing authenticator", zap.Error(err))
		}
	}()
	log.Infow("authenticator initialized")

	var storageClient *storage.Client
	gcsBucket := os.Getenv("GCS_BUCKET")
	if gcsBucket != "" {
		storageClient, err = storage.New(rootCtx, gcsBucket)
		if err != nil {
			log.Warnw("failed to initialize GCS storage client, image uploads will be disabled", zap.Error(err), "bucket", gcsBucket)
		} else {
			defer func() {
				if err := storageClient.Close(); err != nil {
					log.Errorw("error closing storage client", zap.Error(err))
				}
			}()
			log.Infow("GCS storage initialized", "bucket", gcsBucket)
		}
	} else {
		log.Infow("GCS storage not configured, image uploads will be disabled")
	}

	var aiClient *ai.Client
	geminiProject := os.Getenv("GEMINI_PROJECT")
	if geminiProject != "" {
		aiClient, err = ai.NewClient(geminiProject, os.Getenv("GEMINI_LOCATION"))
		if err != nil {
			log.Warnw("failed to initialize AI client", zap.Error(err))
		} else {
			log.Infow("AI client initialized (OCR, transcription enabled)")
		}
	} else {
		log.Infow("AI client not configured (OCR, transcription disabled)")
	}

	imgixDomain := os.Getenv("IMGIX_DOMAIN")

	log.Infow("optional features configured",
		"ai_enabled", aiClient != nil,
		"imgix_enabled", imgixDomain != "",
		"imgix_domain", imgixDomain)

	m2mConfig := auth.NewM2MConfig(rootCtx)

	server := grpc.NewServer(
		grpc.UnaryInterceptor(authInterceptor(authenticator, m2mConfig, log)),
	)

	notesService := service.NewNotesService(database, storageClient, aiClient, imgixDomain)
	tagsService := service.NewTagsService(database)
	authService := service.NewAuthService(database)
	apiKeysService := service.NewApiKeysService(database)
	userSettingsService := service.NewUserSettingsService(database, storageClient, imgixDomain)
	statsService := service.NewStatsService(database)

	pb.RegisterNotesServiceServer(server, notesService)
	pb.RegisterTagsServiceServer(server, tagsService)
	pb.RegisterAuthServiceServer(server, authService)
	pb.RegisterApiKeysServiceServer(server, apiKeysService)
	pb.RegisterUserSettingsServiceServer(server, userSettingsService)
	pb.RegisterStatsServiceServer(server, statsService)

	reflection.Register(server)

	grpcListener, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		log.Errorw("failed to listen on gRPC port", "port", grpcPort, zap.Error(err))
		os.Exit(1)
	}

	// Create HTTP server for health checks.
	// ReadHeaderTimeout is set to protect against slow-loris connections that
	// trickle HTTP request headers without ever completing them. Without it,
	// ReadTimeout alone does not bound header-only connections because
	// ReadTimeout begins only after the first body byte arrives.
	httpServer := &http.Server{
		Addr:              ":" + httpPort,
		Handler:           newHealthHandler(log, promhttp.HandlerFor(registry, promhttp.HandlerOpts{})),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}

	go func() {
		log.Infow("HTTP health server listening", "port", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Errorw("HTTP server error", zap.Error(err))
			os.Exit(1)
		}
	}()

	go func() {
		log.Infow("gRPC server listening", "port", grpcPort)
		if err := server.Serve(grpcListener); err != nil {
			log.Errorw("gRPC server error", zap.Error(err))
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	log.Infow("shutting down servers", "signal", sig.String())

	shutdownCtx, cancel := context.WithTimeout(rootCtx, 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Errorw("HTTP server shutdown error", zap.Error(err))
	}

	server.GracefulStop()

	log.Infow("servers stopped gracefully")
}

// newHealthHandler creates an HTTP handler for health check and metrics
// endpoints. metrics is the Prometheus exposition handler for the OTel
// meter provider configured in main.
func newHealthHandler(log *zap.SugaredLogger, metrics http.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("/metrics", metrics)

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
			log.Errorw("error encoding health response", zap.Error(err))
		}
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"commit": CommitSHA,
		}); err != nil {
			log.Errorw("error encoding health response", zap.Error(err))
		}
	})

	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprint(w, "User-agent: *\nDisallow: /\n"); err != nil {
			log.Errorw("error writing robots", zap.Error(err))
		}
	})

	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status": "ready",
		}); err != nil {
			log.Errorw("error encoding ready response", zap.Error(err))
		}
	})

	return mux
}

// authInterceptor creates a gRPC interceptor that validates API keys and M2M tokens.
// It also injects the application logger into the request context so downstream
// handlers can call logging.FromContext(ctx) and emit correlated logs.
func authInterceptor(authenticator *auth.Authenticator, m2mConfig *auth.M2MConfig, log *zap.SugaredLogger) grpc.UnaryServerInterceptor {
	publicMethods := map[string]bool{
		"/etu.AuthService/Register":        true,
		"/etu.AuthService/Authenticate":    true,
		"/etu.ApiKeysService/VerifyApiKey": true,
	}

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Always seed the per-request logger so downstream code can use logging.FromContext.
		ctx = logging.NewContext(ctx, log)
		l := logging.FromContext(ctx).With("method", info.FullMethod)

		if publicMethods[info.FullMethod] {
			l.Infow("public request")
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		authHeaders := md.Get("authorization")
		if len(authHeaders) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization header")
		}

		token := authHeaders[0]

		if m2mConfig.IsEnabled() {
			if valid, tokenIndex := m2mConfig.ValidateToken(token); valid {
				ctx = auth.SetAuthContext(ctx, "m2m", "m2m")
				m2mConfig.LogAuthentication(ctx, info.FullMethod, tokenIndex)
				return handler(ctx, req)
			}
		}

		userID, err := authenticator.VerifyAPIKey(ctx, token)
		if err != nil {
			l.Warnw("authentication failed", zap.Error(err))
			return nil, status.Errorf(codes.Unauthenticated, "invalid API key: %v", err)
		}

		ctx = auth.SetAuthContext(ctx, userID, "apikey")

		l.Infow("authenticated request", "user_id", userID, "auth_type", "apikey")

		return handler(ctx, req)
	}
}
