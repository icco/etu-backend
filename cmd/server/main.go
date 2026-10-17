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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.icco.me/etu-backend/internal/ai"
	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/billing"
	"go.icco.me/etu-backend/internal/db"
	"go.icco.me/etu-backend/internal/logger"
	"go.icco.me/etu-backend/internal/service"
	"go.icco.me/etu-backend/internal/storage"
	pb "go.icco.me/etu-backend/proto"
	"go.icco.me/gutil/logging"
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

const statusKey = "status"

// maxRecvMsgSize fits a request carrying the maximum number of max-size attachments plus 1MB for note text and framing.
const maxRecvMsgSize = service.MaxImagesPerRequest*service.MaxImageSize + service.MaxAudiosPerRequest*service.MaxAudioSize + 1<<20

func main() {
	log := logger.New("etu-backend-server")
	if err := run(log); err != nil {
		log.Errorw("server exited with error", zap.Error(err))
		os.Exit(1)
	}
}

func run(log *zap.SugaredLogger) error {
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

	registry := prometheus.NewRegistry()
	exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return fmt.Errorf("failed to init prometheus exporter: %w", err)
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
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Errorw("error closing database", zap.Error(err))
		}
	}()

	log.Infow("database initialized")

	authenticator, err := auth.New()
	if err != nil {
		return fmt.Errorf("failed to initialize authenticator: %w", err)
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

	imgixDomain := os.Getenv("MEDIA_BASE_URL")
	if imgixDomain != "" && os.Getenv("MEDIA_SIGNING_KEY") == "" {
		return errors.New("MEDIA_SIGNING_KEY is required with MEDIA_BASE_URL")
	}

	log.Infow("optional features configured",
		"ai_enabled", aiClient != nil,
		"media_enabled", imgixDomain != "",
		"media_base_url", imgixDomain)

	m2mConfig := auth.NewM2MConfig(rootCtx)

	paywall, err := auth.NewPaywallFromEnv()
	if err != nil {
		return fmt.Errorf("failed to configure paywall: %w", err)
	}
	log.Infow("paywall configured", "grace_period", paywall.Grace.String())

	server, err := newGRPCServer(registry, authenticator, m2mConfig, paywall, log)
	if err != nil {
		return err
	}

	notesService := service.NewNotesService(database, storageClient, aiClient, imgixDomain)
	tagsService := service.NewTagsService(database)
	authService := service.NewAuthService(database)
	apiKeysService := service.NewApiKeysService(database)
	userSettingsService := service.NewUserSettingsService(database, storageClient, imgixDomain)
	statsService := service.NewStatsService(database)

	var billingClient service.SubscriptionCanceler
	if key := os.Getenv("STRIPE_SECRET_KEY"); key != "" {
		billingClient = billing.NewStripe(key)
	} else {
		log.Warnw("STRIPE_SECRET_KEY not set; account deletion will refuse users with Stripe subscriptions")
	}
	accountService := service.NewAccountService(database, storageClient, billingClient)

	pb.RegisterNotesServiceServer(server, notesService)
	pb.RegisterTagsServiceServer(server, tagsService)
	pb.RegisterAuthServiceServer(server, authService)
	pb.RegisterApiKeysServiceServer(server, apiKeysService)
	pb.RegisterUserSettingsServiceServer(server, userSettingsService)
	pb.RegisterStatsServiceServer(server, statsService)
	pb.RegisterAccountServiceServer(server, accountService)

	reflection.Register(server)

	var lc net.ListenConfig
	grpcListener, err := lc.Listen(rootCtx, "tcp", ":"+grpcPort)
	if err != nil {
		return fmt.Errorf("failed to listen on gRPC port %s: %w", grpcPort, err)
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
	return nil
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
			statusKey: "ok",
			"commit":  CommitSHA,
		}); err != nil {
			log.Errorw("error encoding health response", zap.Error(err))
		}
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{
			statusKey: "ok",
			"commit":  CommitSHA,
		}); err != nil {
			log.Errorw("error encoding health response", zap.Error(err))
		}
	})

	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprint(w, "User-agent: *\nDisallow: /\n"); err != nil {
			log.Errorw("error writing robots", zap.Error(err))
		}
	})

	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{
			statusKey: "ready",
		}); err != nil {
			log.Errorw("error encoding ready response", zap.Error(err))
		}
	})

	return mux
}

var publicMethods = map[string]bool{
	"/etu.AuthService/Register":        true,
	"/etu.AuthService/Authenticate":    true,
	"/etu.AuthService/Login":           true,
	"/etu.ApiKeysService/VerifyApiKey": true,
}

// authInterceptor creates a gRPC interceptor that validates API keys and M2M tokens.
// It also injects the application logger into the request context so downstream
// handlers can call logging.FromContext(ctx) and emit correlated logs.
func authInterceptor(authenticator *auth.Authenticator, m2mConfig *auth.M2MConfig, log *zap.SugaredLogger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Always seed the per-request logger so downstream code can use logging.FromContext.
		ctx = logging.NewContext(ctx, log)
		l := logging.FromContext(ctx).With("method", info.FullMethod)

		if publicMethods[info.FullMethod] {
			if md, ok := metadata.FromIncomingContext(ctx); ok && m2mConfig.IsEnabled() {
				if tokens := md.Get("authorization"); len(tokens) > 0 {
					if valid, _ := m2mConfig.ValidateToken(tokens[0]); valid {
						ctx = auth.SetAuthContext(ctx, "m2m", "m2m")
					}
				}
			}
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

type authedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s authedServerStream) Context() context.Context { return s.ctx }

// streamAuthInterceptor applies the unary auth interceptor to streaming RPCs so both share one auth path.
func streamAuthInterceptor(unary grpc.UnaryServerInterceptor) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		_, err := unary(ss.Context(), nil, &grpc.UnaryServerInfo{Server: srv, FullMethod: info.FullMethod}, func(ctx context.Context, _ interface{}) (interface{}, error) {
			return nil, handler(srv, authedServerStream{ServerStream: ss, ctx: ctx})
		})
		return err
	}
}
