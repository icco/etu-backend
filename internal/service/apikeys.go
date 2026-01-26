package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ApiKeysService implements the ApiKeysService gRPC service
type ApiKeysService struct {
	pb.UnimplementedApiKeysServiceServer
	db *db.DB
}

// NewApiKeysService creates a new ApiKeysService
func NewApiKeysService(database *db.DB) *ApiKeysService {
	return &ApiKeysService{db: database}
}

// CreateApiKey creates a new API key for a user
func (s *ApiKeysService) CreateApiKey(ctx context.Context, req *pb.CreateApiKeyRequest) (*pb.CreateApiKeyResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	// Generate a random API key: etu_<64 hex characters>
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate random bytes: %v", err)
	}
	rawKey := "etu_" + hex.EncodeToString(randomBytes)

	// Extract prefix for lookup (first 12 chars)
	keyPrefix := rawKey[:12]

	// Hash the full key for storage
	keyHash, err := bcrypt.GenerateFromPassword([]byte(rawKey), bcrypt.DefaultCost)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to hash API key: %v", err)
	}

	// Create the API key in database
	apiKey, err := s.db.CreateApiKey(ctx, req.UserId, req.Name, keyPrefix, string(keyHash))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create API key: %v", err)
	}

	return &pb.CreateApiKeyResponse{
		ApiKey: apiKeyToProto(apiKey),
		RawKey: rawKey,
	}, nil
}

// ListApiKeys lists all API keys for a user
func (s *ApiKeysService) ListApiKeys(ctx context.Context, req *pb.ListApiKeysRequest) (*pb.ListApiKeysResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	keys, err := s.db.ListApiKeys(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list API keys: %v", err)
	}

	pbKeys := make([]*pb.ApiKey, len(keys))
	for i, k := range keys {
		pbKeys[i] = apiKeyToProto(&k)
	}

	return &pb.ListApiKeysResponse{
		ApiKeys: pbKeys,
	}, nil
}

// DeleteApiKey deletes an API key
func (s *ApiKeysService) DeleteApiKey(ctx context.Context, req *pb.DeleteApiKeyRequest) (*pb.DeleteApiKeyResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.KeyId == "" {
		return nil, status.Error(codes.InvalidArgument, "key_id is required")
	}

	deleted, err := s.db.DeleteApiKey(ctx, req.UserId, req.KeyId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete API key: %v", err)
	}

	return &pb.DeleteApiKeyResponse{
		Success: deleted,
	}, nil
}

// VerifyApiKey verifies an API key and returns the associated user ID
func (s *ApiKeysService) VerifyApiKey(ctx context.Context, req *pb.VerifyApiKeyRequest) (*pb.VerifyApiKeyResponse, error) {
	if req.RawKey == "" {
		return nil, status.Error(codes.InvalidArgument, "raw_key is required")
	}

	// Validate key format
	if len(req.RawKey) < 12 || req.RawKey[:4] != "etu_" {
		return &pb.VerifyApiKeyResponse{Valid: false}, nil
	}

	// Extract prefix for lookup
	keyPrefix := req.RawKey[:12]

	// Get potential matching keys
	keys, err := s.db.GetApiKeysByPrefix(ctx, keyPrefix)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to query API keys: %v", err)
	}

	// Check each potential match
	for _, k := range keys {
		if err := bcrypt.CompareHashAndPassword([]byte(k.KeyHash), []byte(req.RawKey)); err == nil {
			// Update last used timestamp asynchronously
			go func(keyID string) {
				_ = s.db.UpdateApiKeyLastUsed(context.Background(), keyID)
			}(k.ID)

			return &pb.VerifyApiKeyResponse{
				Valid:  true,
				UserId: &k.UserID,
			}, nil
		}
	}

	return &pb.VerifyApiKeyResponse{Valid: false}, nil
}

// apiKeyToProto converts a db.ApiKey to a protobuf ApiKey
func apiKeyToProto(k *db.ApiKey) *pb.ApiKey {
	pbKey := &pb.ApiKey{
		Id:        k.ID,
		Name:      k.Name,
		KeyPrefix: k.KeyPrefix,
		CreatedAt: &pb.Timestamp{Seconds: k.CreatedAt.Unix(), Nanos: int32(k.CreatedAt.Nanosecond())},
	}

	if k.LastUsed != nil {
		pbKey.LastUsed = &pb.Timestamp{
			Seconds: k.LastUsed.Unix(),
			Nanos:   int32(k.LastUsed.Nanosecond()),
		}
	}

	return pbKey
}
