package service

import (
	"context"
	"testing"

	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockStatsDB is a mock database for testing stats
type mockStatsDB struct {
	totalBlips   int64
	uniqueTags   int64
	wordsWritten int64
	shouldError  bool
}

func (m *mockStatsDB) GetStats(ctx context.Context, userID string) (int64, int64, int64, error) {
	if m.shouldError {
		return 0, 0, 0, status.Error(codes.Internal, "database error")
	}
	return m.totalBlips, m.uniqueTags, m.wordsWritten, nil
}

// mockStatsService is a mock stats service for testing
type mockStatsService struct {
	pb.UnimplementedStatsServiceServer
	db *mockStatsDB
}

func newMockStatsService(db *mockStatsDB) *mockStatsService {
	return &mockStatsService{
		db: db,
	}
}

func (s *mockStatsService) GetStats(ctx context.Context, req *pb.GetStatsRequest) (*pb.GetStatsResponse, error) {
	totalBlips, uniqueTags, wordsWritten, err := s.db.GetStats(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get stats: %v", err)
	}

	return &pb.GetStatsResponse{
		TotalBlips:   totalBlips,
		UniqueTags:   uniqueTags,
		WordsWritten: wordsWritten,
	}, nil
}

func TestGetStats(t *testing.T) {
	tests := []struct {
		name             string
		req              *pb.GetStatsRequest
		mockDB           *mockStatsDB
		wantErr          codes.Code
		wantTotalBlips   int64
		wantUniqueTags   int64
		wantWordsWritten int64
	}{
		{
			name: "get stats for specific user",
			req: &pb.GetStatsRequest{
				UserId: "user-123",
			},
			mockDB: &mockStatsDB{
				totalBlips:   10,
				uniqueTags:   5,
				wordsWritten: 1500,
			},
			wantErr:          codes.OK,
			wantTotalBlips:   10,
			wantUniqueTags:   5,
			wantWordsWritten: 1500,
		},
		{
			name: "get stats for all users",
			req: &pb.GetStatsRequest{
				UserId: "",
			},
			mockDB: &mockStatsDB{
				totalBlips:   100,
				uniqueTags:   25,
				wordsWritten: 50000,
			},
			wantErr:          codes.OK,
			wantTotalBlips:   100,
			wantUniqueTags:   25,
			wantWordsWritten: 50000,
		},
		{
			name: "user with no notes",
			req: &pb.GetStatsRequest{
				UserId: "user-999",
			},
			mockDB: &mockStatsDB{
				totalBlips:   0,
				uniqueTags:   0,
				wordsWritten: 0,
			},
			wantErr:          codes.OK,
			wantTotalBlips:   0,
			wantUniqueTags:   0,
			wantWordsWritten: 0,
		},
		{
			name: "database error",
			req: &pb.GetStatsRequest{
				UserId: "user-123",
			},
			mockDB: &mockStatsDB{
				shouldError: true,
			},
			wantErr: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newMockStatsService(tt.mockDB)
			ctx := context.Background()

			resp, err := svc.GetStats(ctx, tt.req)

			if tt.wantErr == codes.OK {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if resp == nil {
					t.Error("expected response")
					return
				}
				if resp.TotalBlips != tt.wantTotalBlips {
					t.Errorf("expected TotalBlips=%d, got %d", tt.wantTotalBlips, resp.TotalBlips)
				}
				if resp.UniqueTags != tt.wantUniqueTags {
					t.Errorf("expected UniqueTags=%d, got %d", tt.wantUniqueTags, resp.UniqueTags)
				}
				if resp.WordsWritten != tt.wantWordsWritten {
					t.Errorf("expected WordsWritten=%d, got %d", tt.wantWordsWritten, resp.WordsWritten)
				}
			} else {
				if err == nil {
					t.Error("expected error, got nil")
				}
				st, ok := status.FromError(err)
				if !ok {
					t.Errorf("expected gRPC status error, got %v", err)
				}
				if st.Code() != tt.wantErr {
					t.Errorf("expected error code %v, got %v", tt.wantErr, st.Code())
				}
			}
		})
	}
}
