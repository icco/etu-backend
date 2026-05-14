package service

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/icco/etu-backend/internal/auth"
	"github.com/icco/etu-backend/internal/db"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// userColumns lists all columns of the User table in the order GORM scans them.
var userColumns = []string{
	"id", colEmail, "name", "image", colPasswordHash, colSubscriptionStatus,
	"subscriptionEnd", colCreatedAt, "stripeCustomerId", "notionKey",
	"notionDatabaseName", "profileImageGCSObject", colUpdatedAt,
	colDisabled, "disabledReason", colFailedLoginAttempts, "lastFailedLogin",
}

// testProfileImageGCSObject is a fixture GCS object name reused across the
// profile-image fixtures below.
const testProfileImageGCSObject = "profiles/user1/avatar"

// helper to create a sqlmock-backed UserSettingsService.
func newTestUserSettingsService(t *testing.T, imgixDomain string) (*UserSettingsService, sqlmock.Sqlmock, func()) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	database, err := db.NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	svc := NewUserSettingsService(database, nil, imgixDomain)
	cleanup := func() { _ = sqlDB.Close() }
	return svc, mock, cleanup
}

func strPtr(s string) *string { return &s }

// ---------- GetUserSettings ----------

func TestGetUserSettings_Basic(t *testing.T) {
	svc, mock, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), testUserNameRow, "m2m")
	now := time.Now()

	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs(testUserNameRow, 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			testUserNameRow, "a@b.com", "Alice", "https://img.example/old.png", "hash",
			"active", nil, now, nil, nil, nil, nil, now,
			false, nil, 0, nil,
		))

	resp, err := svc.GetUserSettings(ctx, &pb.GetUserSettingsRequest{UserId: testUserNameRow})
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if resp.User == nil {
		t.Fatal("expected user in response")
	}
	if resp.User.Name == nil || *resp.User.Name != "Alice" {
		t.Errorf("expected name Alice, got %v", resp.User.Name)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetUserSettings_MissingUserID(t *testing.T) {
	svc, _, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), testUserNameRow, "m2m")

	_, err := svc.GetUserSettings(ctx, &pb.GetUserSettingsRequest{UserId: ""})
	if err == nil {
		t.Fatal("expected error for empty user_id")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGetUserSettings_ImageReturnsGCSKey(t *testing.T) {
	// User.Image carries the storage object key — clients call
	// GetProfileImageURL to resolve it to a renderable URL.
	svc, mock, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), testUserNameRow, "m2m")
	now := time.Now()
	gcsObj := testProfileImageGCSObject

	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs(testUserNameRow, 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			testUserNameRow, "a@b.com", "Alice", nil, "hash",
			"active", nil, now, nil, nil, nil, &gcsObj, now,
			false, nil, 0, nil,
		))

	resp, err := svc.GetUserSettings(ctx, &pb.GetUserSettingsRequest{UserId: testUserNameRow})
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}

	if resp.User.Image == nil || *resp.User.Image != gcsObj {
		t.Errorf("expected image=%q, got %v", gcsObj, resp.User.Image)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// ---------- UpdateUserSettings ----------

func TestUpdateUserSettings_MissingUserID(t *testing.T) {
	svc, _, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), testUserNameRow, "m2m")

	_, err := svc.UpdateUserSettings(ctx, &pb.UpdateUserSettingsRequest{UserId: ""})
	if err == nil {
		t.Fatal("expected error for empty user_id")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestUpdateUserSettings_ProfileImageUpload_NilStorage(t *testing.T) {
	svc, _, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), testUserNameRow, "m2m")

	_, err := svc.UpdateUserSettings(ctx, &pb.UpdateUserSettingsRequest{
		UserId: testUserNameRow,
		ProfileImageUpload: &pb.ImageUpload{
			Data:     []byte{0x89, 0x50, 0x4E, 0x47}, // PNG magic bytes (partial)
			MimeType: "image/png",
		},
	})
	if err == nil {
		t.Fatal("expected error when storage is nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", st.Code())
	}
}

func TestUpdateUserSettings_NoImageFieldInProto(t *testing.T) {
	// The `image` field (5) has been reserved in the proto. UpdateUserSettingsRequest
	// no longer has an Image field. Without ProfileImageUpload, the image and
	// profileImageGCSObject remain nil and the DB update only touches updatedAt.
	svc, mock, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), testUserNameRow, "m2m")
	now := time.Now()

	// Step 1: SELECT to find the existing user
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs(testUserNameRow, 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			testUserNameRow, "a@b.com", "Alice", "https://old.example/img.png", "hash",
			"active", nil, now, nil, nil, nil, nil, now,
			false, nil, 0, nil,
		))
	// Step 2: UPDATE — only updatedAt changes
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(sqlmock.AnyArg(), testUserNameRow).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	// Step 3: SELECT to reload user
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs(testUserNameRow, testUserNameRow, 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			testUserNameRow, "a@b.com", "Alice", "https://old.example/img.png", "hash",
			"active", nil, now, nil, nil, nil, nil, now,
			false, nil, 0, nil,
		))

	// Request has no image fields; row has no profileImageGCSObject, so
	// resp.User.Image stays nil.
	resp, err := svc.UpdateUserSettings(ctx, &pb.UpdateUserSettingsRequest{
		UserId: testUserNameRow,
	})
	if err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	if resp.User == nil {
		t.Fatal("expected user in response")
	}
	if resp.User.Image != nil {
		t.Errorf("expected nil image, got %v", *resp.User.Image)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestUpdateUserSettings_NameOnly(t *testing.T) {
	svc, mock, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), testUserNameRow, "m2m")
	now := time.Now()
	newName := "Bob"

	// Step 1: SELECT to find the existing user
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs(testUserNameRow, 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			testUserNameRow, "a@b.com", "Alice", nil, "hash",
			"active", nil, now, nil, nil, nil, nil, now,
			false, nil, 0, nil,
		))
	// Step 2: UPDATE via Model.Updates
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(),
			testUserNameRow,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	// Step 3: SELECT to reload user (GORM adds "User"."id" = $2 from the model)
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs(testUserNameRow, testUserNameRow, 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			testUserNameRow, "a@b.com", &newName, nil, "hash",
			"active", nil, now, nil, nil, nil, nil, now,
			false, nil, 0, nil,
		))

	resp, err := svc.UpdateUserSettings(ctx, &pb.UpdateUserSettingsRequest{
		UserId: testUserNameRow,
		Name:   &newName,
	})
	if err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	if resp.User == nil {
		t.Fatal("expected user in response")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestUpdateUserSettings_ClearProfileImage(t *testing.T) {
	svc, mock, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), testUserNameRow, "m2m")
	now := time.Now()
	clearFlag := true

	// Step 1: SELECT existing user (has a profile image)
	gcsObj := testProfileImageGCSObject
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs(testUserNameRow, 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			testUserNameRow, "a@b.com", "Alice", "https://signed.example/img", "hash",
			"active", nil, now, nil, nil, nil, &gcsObj, now,
			false, nil, 0, nil,
		))
	// Step 2: UPDATE — clears profileImageGCSObject only.
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs("", sqlmock.AnyArg(), testUserNameRow).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	// Step 3: SELECT to reload user
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs(testUserNameRow, testUserNameRow, 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			testUserNameRow, "a@b.com", "Alice", "", "hash",
			"active", nil, now, nil, nil, nil, "", now,
			false, nil, 0, nil,
		))

	resp, err := svc.UpdateUserSettings(ctx, &pb.UpdateUserSettingsRequest{
		UserId:            testUserNameRow,
		ClearProfileImage: &clearFlag,
	})
	if err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	if resp.User == nil {
		t.Fatal("expected user in response")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// ---------- GetProfileImageURL ----------

func TestGetProfileImageURL_MissingKey(t *testing.T) {
	svc, _, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	_, err := svc.GetProfileImageURL(context.Background(), &pb.GetProfileImageURLRequest{Key: ""})
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if st, _ := status.FromError(err); st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGetProfileImageURL_Imgix(t *testing.T) {
	svc, _, cleanup := newTestUserSettingsService(t, "my-cdn.imgix.net")
	defer cleanup()

	resp, err := svc.GetProfileImageURL(context.Background(), &pb.GetProfileImageURLRequest{Key: testProfileImageGCSObject})
	if err != nil {
		t.Fatalf("GetProfileImageURL: %v", err)
	}
	expected := "https://my-cdn.imgix.net/profiles/user1/avatar"
	if resp.Url != expected {
		t.Errorf("expected %q, got %q", expected, resp.Url)
	}
}

func TestGetProfileImageURL_NoImgixNoStorage(t *testing.T) {
	svc, _, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	_, err := svc.GetProfileImageURL(context.Background(), &pb.GetProfileImageURLRequest{Key: testProfileImageGCSObject})
	if err == nil {
		t.Fatal("expected error when storage is nil and no imgix")
	}
	if st, _ := status.FromError(err); st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", st.Code())
	}
}
