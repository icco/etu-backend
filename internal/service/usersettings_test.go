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
	"id", "email", "name", "image", "passwordHash", "subscriptionStatus",
	"subscriptionEnd", "createdAt", "stripeCustomerId", "notionKey",
	"notionDatabaseName", "profileImageGCSObject", "updatedAt",
	"disabled", "disabledReason", "failedLoginAttempts", "lastFailedLogin",
}

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

	ctx := auth.SetAuthContext(context.Background(), "user1", "m2m")
	now := time.Now()

	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user1", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			"user1", "a@b.com", "Alice", "https://img.example/old.png", "hash",
			"active", nil, now, nil, nil, nil, nil, now,
			false, nil, 0, nil,
		))

	resp, err := svc.GetUserSettings(ctx, &pb.GetUserSettingsRequest{UserId: "user1"})
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

	ctx := auth.SetAuthContext(context.Background(), "user1", "m2m")

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

func TestGetUserSettings_RefreshSignedURL_ImgixDomain(t *testing.T) {
	svc, mock, cleanup := newTestUserSettingsService(t, "my-imgix.imgix.net")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), "user1", "m2m")
	now := time.Now()
	gcsObj := "profiles/user1/avatar"

	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user1", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			"user1", "a@b.com", "Alice", "https://old-signed-url.example", "hash",
			"active", nil, now, nil, nil, nil, &gcsObj, now,
			false, nil, 0, nil,
		))

	resp, err := svc.GetUserSettings(ctx, &pb.GetUserSettingsRequest{UserId: "user1"})
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}

	expected := "https://my-imgix.imgix.net/profiles/user1/avatar"
	if resp.User.Image == nil || *resp.User.Image != expected {
		t.Errorf("expected image %q, got %v", expected, resp.User.Image)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetUserSettings_RefreshSignedURL_NoImgixNoStorage(t *testing.T) {
	// When there is no imgix domain and storage is nil, the image should remain
	// unchanged even if ProfileImageGCSObject is set (the storage.GetSignedURL
	// path is skipped because storage == nil).
	svc, mock, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), "user1", "m2m")
	now := time.Now()
	gcsObj := "profiles/user1/avatar"
	oldImage := "https://old-signed-url.example"

	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user1", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			"user1", "a@b.com", "Alice", &oldImage, "hash",
			"active", nil, now, nil, nil, nil, &gcsObj, now,
			false, nil, 0, nil,
		))

	resp, err := svc.GetUserSettings(ctx, &pb.GetUserSettingsRequest{UserId: "user1"})
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}

	// Image should remain the old value since we can't refresh without storage or imgix
	if resp.User.Image == nil || *resp.User.Image != oldImage {
		t.Errorf("expected image unchanged %q, got %v", oldImage, resp.User.Image)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// ---------- UpdateUserSettings ----------

func TestUpdateUserSettings_MissingUserID(t *testing.T) {
	svc, _, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), "user1", "m2m")

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

	ctx := auth.SetAuthContext(context.Background(), "user1", "m2m")

	_, err := svc.UpdateUserSettings(ctx, &pb.UpdateUserSettingsRequest{
		UserId: "user1",
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

	ctx := auth.SetAuthContext(context.Background(), "user1", "m2m")
	now := time.Now()

	// Step 1: SELECT to find the existing user
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user1", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			"user1", "a@b.com", "Alice", "https://old.example/img.png", "hash",
			"active", nil, now, nil, nil, nil, nil, now,
			false, nil, 0, nil,
		))
	// Step 2: UPDATE — only updatedAt changes
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(sqlmock.AnyArg(), "user1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	// Step 3: SELECT to reload user
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user1", "user1", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			"user1", "a@b.com", "Alice", "https://old.example/img.png", "hash",
			"active", nil, now, nil, nil, nil, nil, now,
			false, nil, 0, nil,
		))

	// Send a request with no image-related fields — only name is absent too,
	// so the DB update should only set updatedAt.
	resp, err := svc.UpdateUserSettings(ctx, &pb.UpdateUserSettingsRequest{
		UserId: "user1",
	})
	if err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	if resp.User == nil {
		t.Fatal("expected user in response")
	}
	// Image should be unchanged
	if resp.User.Image == nil || *resp.User.Image != "https://old.example/img.png" {
		t.Errorf("expected image unchanged, got %v", resp.User.Image)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestUpdateUserSettings_NameOnly(t *testing.T) {
	svc, mock, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), "user1", "m2m")
	now := time.Now()
	newName := "Bob"

	// Step 1: SELECT to find the existing user
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user1", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			"user1", "a@b.com", "Alice", nil, "hash",
			"active", nil, now, nil, nil, nil, nil, now,
			false, nil, 0, nil,
		))
	// Step 2: UPDATE via Model.Updates
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(),
			"user1",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	// Step 3: SELECT to reload user (GORM adds "User"."id" = $2 from the model)
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user1", "user1", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			"user1", "a@b.com", &newName, nil, "hash",
			"active", nil, now, nil, nil, nil, nil, now,
			false, nil, 0, nil,
		))

	resp, err := svc.UpdateUserSettings(ctx, &pb.UpdateUserSettingsRequest{
		UserId: "user1",
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

	ctx := auth.SetAuthContext(context.Background(), "user1", "m2m")
	now := time.Now()
	clearFlag := true

	// Step 1: SELECT existing user (has a profile image)
	gcsObj := "profiles/user1/avatar"
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user1", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			"user1", "a@b.com", "Alice", "https://signed.example/img", "hash",
			"active", nil, now, nil, nil, nil, &gcsObj, now,
			false, nil, 0, nil,
		))
	// Step 2: UPDATE — clears image and profileImageGCSObject
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "User"`).
		WithArgs("", "", sqlmock.AnyArg(), "user1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	// Step 3: SELECT to reload user
	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs("user1", "user1", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			"user1", "a@b.com", "Alice", "", "hash",
			"active", nil, now, nil, nil, nil, "", now,
			false, nil, 0, nil,
		))

	resp, err := svc.UpdateUserSettings(ctx, &pb.UpdateUserSettingsRequest{
		UserId:            "user1",
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

// ---------- refreshProfileImageURL ----------

func TestRefreshProfileImageURL_NilObject(t *testing.T) {
	svc, _, cleanup := newTestUserSettingsService(t, "cdn.example.com")
	defer cleanup()

	user := &db.User{
		ID:                    "user1",
		Image:                 strPtr("https://original.example/img.png"),
		ProfileImageGCSObject: nil,
	}

	svc.refreshProfileImageURL(context.Background(), user)

	// Image should remain unchanged when GCS object is nil
	if *user.Image != "https://original.example/img.png" {
		t.Errorf("expected image unchanged, got %s", *user.Image)
	}
}

func TestRefreshProfileImageURL_EmptyObject(t *testing.T) {
	svc, _, cleanup := newTestUserSettingsService(t, "cdn.example.com")
	defer cleanup()

	empty := ""
	user := &db.User{
		ID:                    "user1",
		Image:                 strPtr("https://original.example/img.png"),
		ProfileImageGCSObject: &empty,
	}

	svc.refreshProfileImageURL(context.Background(), user)

	// Image should remain unchanged when GCS object is empty string
	if *user.Image != "https://original.example/img.png" {
		t.Errorf("expected image unchanged, got %s", *user.Image)
	}
}

func TestRefreshProfileImageURL_WithImgixDomain(t *testing.T) {
	svc, _, cleanup := newTestUserSettingsService(t, "my-cdn.imgix.net")
	defer cleanup()

	gcsObj := "profiles/user1/avatar"
	user := &db.User{
		ID:                    "user1",
		Image:                 strPtr("https://old.example/img.png"),
		ProfileImageGCSObject: &gcsObj,
	}

	svc.refreshProfileImageURL(context.Background(), user)

	expected := "https://my-cdn.imgix.net/profiles/user1/avatar"
	if *user.Image != expected {
		t.Errorf("expected %q, got %q", expected, *user.Image)
	}
}

func TestRefreshProfileImageURL_NoImgixNoStorage(t *testing.T) {
	// storage is nil, imgixDomain is empty — image should remain unchanged
	svc, _, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	gcsObj := "profiles/user1/avatar"
	oldImg := "https://old-signed.example/img.png"
	user := &db.User{
		ID:                    "user1",
		Image:                 &oldImg,
		ProfileImageGCSObject: &gcsObj,
	}

	svc.refreshProfileImageURL(context.Background(), user)

	if *user.Image != oldImg {
		t.Errorf("expected image unchanged %q, got %q", oldImg, *user.Image)
	}
}
