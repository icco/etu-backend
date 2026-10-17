package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.icco.me/etu-backend/internal/auth"
	pb "go.icco.me/etu-backend/proto"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type metricsService struct {
	pb.UnimplementedAuthServiceServer
	pb.UnimplementedAccountServiceServer
}

func (metricsService) Authenticate(_ context.Context, req *pb.AuthenticateRequest) (*pb.AuthenticateResponse, error) {
	if req.Email == "panic" {
		panic("private detail")
	}
	return &pb.AuthenticateResponse{}, nil
}

func (metricsService) ExportData(req *pb.ExportDataRequest, stream grpc.ServerStreamingServer[pb.ExportDataResponse]) error {
	if req.UserId == "panic" {
		panic("private detail")
	}
	return stream.Send(&pb.ExportDataResponse{})
}

func TestGRPCMetrics(t *testing.T) {
	t.Setenv("GRPC_API_KEYS", "test-service-token")
	registry := prometheus.NewRegistry()
	log := zap.NewNop().Sugar()
	server, err := newGRPCServer(registry, nil, auth.NewM2MConfig(context.Background()), auth.Paywall{}, log)
	if err != nil {
		t.Fatal(err)
	}
	pb.RegisterAuthServiceServer(server, metricsService{})
	pb.RegisterAccountServiceServer(server, metricsService{})
	lis := bufconn.Listen(1024 * 1024)
	done := make(chan error, 1)
	go func() { done <- server.Serve(lis) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///metrics-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := pb.NewAuthServiceClient(conn)
	if _, err := client.Authenticate(ctx, &pb.AuthenticateRequest{Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Authenticate(ctx, &pb.AuthenticateRequest{Email: "panic"}); status.Code(err) != codes.Internal {
		t.Fatalf("panic code: %v", err)
	}
	if _, err := client.GetUser(ctx, &pb.GetUserRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("auth denial: %v", err)
	}
	account := pb.NewAccountServiceClient(conn)
	for _, tt := range []struct {
		name string
		ctx  context.Context
		user string
		want codes.Code
	}{
		{"success", metadata.AppendToOutgoingContext(ctx, "authorization", "test-service-token"), "alice", codes.OK},
		{"panic", metadata.AppendToOutgoingContext(ctx, "authorization", "test-service-token"), "panic", codes.Internal},
		{"denied", ctx, "alice", codes.Unauthenticated},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stream, err := account.ExportData(tt.ctx, &pb.ExportDataRequest{UserId: tt.user})
			if err != nil {
				t.Fatal(err)
			}
			_, err = stream.Recv()
			if status.Code(err) != tt.want {
				t.Fatalf("stream result: %v", err)
			}
			if err == nil {
				if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
					t.Fatalf("stream end: %v", err)
				}
			}
		})
	}
	w := httptest.NewRecorder()
	newHealthHandler(log, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}), http.NotFoundHandler()).ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("scrape status %d", w.Code)
	}
	for _, line := range []string{
		`grpc_server_handled_total{grpc_code="OK",grpc_method="Authenticate",grpc_service="etu.AuthService",grpc_type="unary"} 1`,
		`grpc_server_handled_total{grpc_code="Internal",grpc_method="Authenticate",grpc_service="etu.AuthService",grpc_type="unary"} 1`,
		`grpc_server_handled_total{grpc_code="Unauthenticated",grpc_method="GetUser",grpc_service="etu.AuthService",grpc_type="unary"} 1`,
		`grpc_server_handled_total{grpc_code="OK",grpc_method="ExportData",grpc_service="etu.AccountService",grpc_type="server_stream"} 1`,
		`grpc_server_handled_total{grpc_code="Internal",grpc_method="ExportData",grpc_service="etu.AccountService",grpc_type="server_stream"} 1`,
		`grpc_server_handled_total{grpc_code="Unauthenticated",grpc_method="ExportData",grpc_service="etu.AccountService",grpc_type="server_stream"} 1`,
		`grpc_server_handling_seconds_count{grpc_method="Authenticate",grpc_service="etu.AuthService",grpc_type="unary"} 2`,
		`grpc_server_handling_seconds_count{grpc_method="ExportData",grpc_service="etu.AccountService",grpc_type="server_stream"} 3`,
	} {
		if !strings.Contains(w.Body.String(), line+"\n") {
			t.Errorf("missing metric %s", line)
		}
	}
	for _, private := range []string{"alice", "test-service-token", "private detail"} {
		if strings.Contains(w.Body.String(), private) {
			t.Errorf("metrics exposed %q", private)
		}
	}
}
