package main

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRecoveryUnaryInterceptor(t *testing.T) {
	tests := []struct {
		name     string
		handler  grpc.UnaryHandler
		wantCode codes.Code
		wantMsg  string
		wantLogs int
	}{
		{
			name:     "panic is recovered",
			handler:  func(context.Context, any) (any, error) { panic("secret detail") },
			wantCode: codes.Internal,
			wantMsg:  "internal error",
			wantLogs: 1,
		},
		{
			name:     "no panic passes through",
			handler:  func(context.Context, any) (any, error) { return "ok", nil },
			wantCode: codes.OK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zap.ErrorLevel)
			unary, _ := recoveryInterceptors(zap.New(core).Sugar())

			_, err := unary(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/etu.NotesService/GetNote"}, tt.handler)
			if diff := cmp.Diff(tt.wantCode, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s", diff)
			}
			if err != nil {
				if diff := cmp.Diff(tt.wantMsg, status.Convert(err).Message()); diff != "" {
					t.Errorf("message mismatch (-want +got):\n%s", diff)
				}
			}
			if diff := cmp.Diff(tt.wantLogs, logs.Len()); diff != "" {
				t.Fatalf("log count mismatch (-want +got):\n%s", diff)
			}
			if tt.wantLogs > 0 {
				fields := logs.All()[0].ContextMap()
				if !strings.Contains(fields["stack"].(string), "goroutine") {
					t.Errorf("stack not logged: %v", fields)
				}
			}
		})
	}
}

type fakeStream struct{ grpc.ServerStream }

func (fakeStream) Context() context.Context { return context.Background() }

func TestRecoveryStreamInterceptor(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	_, stream := recoveryInterceptors(zap.New(core).Sugar())

	err := stream(nil, fakeStream{}, &grpc.StreamServerInfo{FullMethod: "/etu.X/Y"}, func(any, grpc.ServerStream) error { panic("boom") })
	if diff := cmp.Diff(codes.Internal, status.Code(err)); diff != "" {
		t.Errorf("code mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, logs.Len()); diff != "" {
		t.Errorf("log count mismatch (-want +got):\n%s", diff)
	}
}
