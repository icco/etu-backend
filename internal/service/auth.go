package service

import (
	"context"
	"time"

	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AuthService implements the AuthService gRPC service
type AuthService struct {
	pb.UnimplementedAuthServiceServer
	db *db.DB
}

// NewAuthService creates a new AuthService
func NewAuthService(database *db.DB) *AuthService {
	return &AuthService{db: database}
}

// Register creates a new user account
func (s *AuthService) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	if req.Email == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}
	if req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "password is required")
	}

	// Check if user already exists
	existingUser, err := s.db.GetUserByEmail(ctx, req.Email)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to check existing user: %v", err)
	}
	if existingUser != nil {
		return nil, status.Error(codes.AlreadyExists, "user with this email already exists")
	}

	// Hash the password
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to hash password: %v", err)
	}

	// Create the user
	user, err := s.db.CreateUser(ctx, req.Email, string(passwordHash))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create user: %v", err)
	}

	return &pb.RegisterResponse{
		User: userToProto(user),
	}, nil
}

// Authenticate verifies user credentials
func (s *AuthService) Authenticate(ctx context.Context, req *pb.AuthenticateRequest) (*pb.AuthenticateResponse, error) {
	if req.Email == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}
	if req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "password is required")
	}

	// Get user by email
	user, err := s.db.GetUserByEmail(ctx, req.Email)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get user: %v", err)
	}
	if user == nil {
		return &pb.AuthenticateResponse{Success: false}, nil
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return &pb.AuthenticateResponse{Success: false}, nil
	}

	return &pb.AuthenticateResponse{
		Success: true,
		User:    userToProto(user),
	}, nil
}

// GetUser retrieves a user by ID
func (s *AuthService) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.GetUserResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	user, err := s.db.GetUser(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get user: %v", err)
	}
	if user == nil {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	return &pb.GetUserResponse{
		User: userToProto(user),
	}, nil
}

// GetUserByStripeCustomerId retrieves a user by Stripe customer ID
func (s *AuthService) GetUserByStripeCustomerId(ctx context.Context, req *pb.GetUserByStripeCustomerIdRequest) (*pb.GetUserByStripeCustomerIdResponse, error) {
	if req.StripeCustomerId == "" {
		return nil, status.Error(codes.InvalidArgument, "stripe_customer_id is required")
	}

	user, err := s.db.GetUserByStripeCustomerID(ctx, req.StripeCustomerId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get user: %v", err)
	}

	var protoUser *pb.User
	if user != nil {
		protoUser = userToProto(user)
	}

	return &pb.GetUserByStripeCustomerIdResponse{
		User: protoUser,
	}, nil
}

// UpdateUserSubscription updates a user's subscription information
func (s *AuthService) UpdateUserSubscription(ctx context.Context, req *pb.UpdateUserSubscriptionRequest) (*pb.UpdateUserSubscriptionResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.SubscriptionStatus == "" {
		return nil, status.Error(codes.InvalidArgument, "subscription_status is required")
	}

	var stripeCustomerID *string
	if req.StripeCustomerId != nil {
		stripeCustomerID = req.StripeCustomerId
	}

	var subscriptionEnd *time.Time
	if req.SubscriptionEnd != nil {
		t := time.Unix(req.SubscriptionEnd.Seconds, int64(req.SubscriptionEnd.Nanos))
		subscriptionEnd = &t
	}

	user, err := s.db.UpdateUserSubscription(ctx, req.UserId, req.SubscriptionStatus, stripeCustomerID, subscriptionEnd)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update user subscription: %v", err)
	}
	if user == nil {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	return &pb.UpdateUserSubscriptionResponse{
		User: userToProto(user),
	}, nil
}

// userToProto converts a db.User to a protobuf User
func userToProto(u *db.User) *pb.User {
	pbUser := &pb.User{
		Id:                 u.ID,
		Email:              u.Email,
		SubscriptionStatus: u.SubscriptionStatus,
		CreatedAt:          &pb.Timestamp{Seconds: u.CreatedAt.Unix(), Nanos: int32(u.CreatedAt.Nanosecond())},
	}

	if u.Name != nil {
		pbUser.Name = u.Name
	}
	if u.Image != nil {
		pbUser.Image = u.Image
	}
	if u.SubscriptionEnd != nil {
		pbUser.SubscriptionEnd = &pb.Timestamp{
			Seconds: u.SubscriptionEnd.Unix(),
			Nanos:   int32(u.SubscriptionEnd.Nanosecond()),
		}
	}
	if u.StripeCustomerID != nil {
		pbUser.StripeCustomerId = u.StripeCustomerID
	}

	return pbUser
}
