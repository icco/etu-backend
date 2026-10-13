package service

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/db"
	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// userColumns lists all columns of the User table in the order GORM scans them.
var userColumns = []string{
	"id", colEmail, "name", colPasswordHash, colSubscriptionStatus,
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

// ---------- GetUserSettings ----------

func TestGetUserSettings_Basic(t *testing.T) {
	svc, mock, cleanup := newTestUserSettingsService(t, "")
	defer cleanup()

	ctx := auth.SetAuthContext(context.Background(), testUserNameRow, "m2m")
	now := time.Now()

	mock.ExpectQuery(`SELECT \* FROM "User"`).
		WithArgs(testUserNameRow, 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
			testUserNameRow, "a@b.com", "Alice", "hash",
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
			testUserNameRow, "a@b.com", "Alice", "hash",
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
			testUserNameRow, "a@b.com", "Alice", "hash",
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
			testUserNameRow, "a@b.com", "Alice", "hash",
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
			testUserNameRow, "a@b.com", "Alice", "hash",
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
			testUserNameRow, "a@b.com", &newName, "hash",
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
			testUserNameRow, "a@b.com", "Alice", "hash",
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
			testUserNameRow, "a@b.com", "Alice", "hash",
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

func TestUserToProtoNotionKey(t *testing.T) {
	key := "secret_notion"
	empty := ""
	tests := []struct {
		name          string
		notionKey     *string
		wantHasNotion bool
	}{
		{name: "no key", notionKey: nil},
		{name: "empty key", notionKey: &empty},
		{name: "key set", notionKey: &key, wantHasNotion: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := userToProto(&db.User{ID: testUserID1, NotionKey: tt.notionKey})
			if got.NotionKey != nil {
				t.Errorf("NotionKey = %q, want nil", *got.NotionKey)
			}
			if got.HasNotionKey != tt.wantHasNotion {
				t.Errorf("HasNotionKey = %v, want %v", got.HasNotionKey, tt.wantHasNotion)
			}
		})
	}
}

func TestGetProfileImageURL(t *testing.T) {
	m2m := auth.SetAuthContext(context.Background(), "m2m", "m2m")
	owner := auth.SetAuthContext(context.Background(), testUserNameRow, "apikey")
	other := auth.SetAuthContext(context.Background(), testUserID2, "apikey")
	stored := testProfileImageGCSObject
	otherImage := "profiles/user-456/avatar"

	tests := []struct {
		name       string
		ctx        context.Context
		req        *pb.GetProfileImageURLRequest
		imgix      string
		lookupUser string
		image      *string
		wantErr    codes.Code
	}{
		{name: "apikey own image by key", ctx: owner, req: &pb.GetProfileImageURLRequest{Key: stored}, imgix: "https://images.example.com/etu", lookupUser: testUserNameRow, image: &stored},
		{name: "apikey own image without key", ctx: owner, req: &pb.GetProfileImageURLRequest{}, imgix: "https://images.example.com/etu", lookupUser: testUserNameRow, image: &stored},
		{name: "apikey other user's key denied", ctx: other, req: &pb.GetProfileImageURLRequest{Key: stored}, imgix: "https://images.example.com/etu", lookupUser: testUserID2, image: &otherImage, wantErr: codes.PermissionDenied},
		{name: "apikey other user_id denied", ctx: other, req: &pb.GetProfileImageURLRequest{UserId: testUserNameRow}, imgix: "https://images.example.com/etu", wantErr: codes.PermissionDenied},
		{name: "apikey arbitrary key denied", ctx: owner, req: &pb.GetProfileImageURLRequest{Key: "notes/secret/blob"}, imgix: "https://images.example.com/etu", lookupUser: testUserNameRow, image: &stored, wantErr: codes.PermissionDenied},
		{name: "unauthenticated", ctx: context.Background(), req: &pb.GetProfileImageURLRequest{Key: stored}, wantErr: codes.Unauthenticated},
		{name: "m2m with user_id", ctx: m2m, req: &pb.GetProfileImageURLRequest{UserId: testUserNameRow}, imgix: "https://images.example.com/etu", lookupUser: testUserNameRow, image: &stored},
		{name: "m2m derives user from key", ctx: m2m, req: &pb.GetProfileImageURLRequest{Key: stored}, imgix: "https://images.example.com/etu", lookupUser: testUserNameRow, image: &stored},
		{name: "m2m non-profile key rejected", ctx: m2m, req: &pb.GetProfileImageURLRequest{Key: "notes/secret/blob"}, wantErr: codes.InvalidArgument},
		{name: "m2m missing key and user", ctx: m2m, req: &pb.GetProfileImageURLRequest{}, wantErr: codes.InvalidArgument},
		{name: "no image stored", ctx: owner, req: &pb.GetProfileImageURLRequest{}, lookupUser: testUserNameRow, wantErr: codes.NotFound},
		{name: "no imgix no storage", ctx: owner, req: &pb.GetProfileImageURLRequest{}, lookupUser: testUserNameRow, image: &stored, wantErr: codes.FailedPrecondition},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MEDIA_SIGNING_KEY", "test-key")
			svc, mock, cleanup := newTestUserSettingsService(t, tt.imgix)
			defer cleanup()

			if tt.lookupUser != "" {
				now := time.Now()
				mock.ExpectQuery(`SELECT \* FROM "User"`).
					WithArgs(tt.lookupUser, 1).
					WillReturnRows(sqlmock.NewRows(userColumns).AddRow(
						tt.lookupUser, "a@b.com", "Alice", "hash",
						"active", nil, now, nil, nil, nil, tt.image, now,
						false, nil, 0, nil,
					))
			}

			resp, err := svc.GetProfileImageURL(tt.ctx, tt.req)
			if got := status.Code(err); got != tt.wantErr {
				t.Fatalf("GetProfileImageURL code = %v, want %v (err %v)", got, tt.wantErr, err)
			}
			if tt.wantErr == codes.OK {
				u, err := url.Parse(resp.GetUrl())
				if err != nil {
					t.Fatalf("parse url: %v", err)
				}
				if diff := cmp.Diff("/etu/"+stored, u.Path); diff != "" {
					t.Errorf("url mismatch (-want +got):\n%s", diff)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled expectations: %v", err)
			}
		})
	}
}

func TestUpdateUserSettings_AIEnabled(t *testing.T) {
	aiColumns := append(append([]string{}, userColumns...), "aiEnabled")
	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "opt in", enabled: true},
		{name: "opt out", enabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mock, cleanup := newTestUserSettingsService(t, "")
			defer cleanup()

			ctx := auth.SetAuthContext(context.Background(), testUserNameRow, "m2m")
			now := time.Now()
			row := func(ai bool) *sqlmock.Rows {
				return sqlmock.NewRows(aiColumns).AddRow(
					testUserNameRow, "a@b.com", "Alice", "hash",
					"active", nil, now, nil, nil, nil, nil, now,
					false, nil, 0, nil, ai,
				)
			}

			mock.ExpectQuery(`SELECT \* FROM "User"`).
				WithArgs(testUserNameRow, 1).
				WillReturnRows(row(!tt.enabled))
			mock.ExpectBegin()
			mock.ExpectExec(`UPDATE "User" SET "aiEnabled"=\$1,"updatedAt"=\$2 WHERE "id" = \$3`).
				WithArgs(tt.enabled, sqlmock.AnyArg(), testUserNameRow).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			mock.ExpectQuery(`SELECT \* FROM "User"`).
				WithArgs(testUserNameRow, testUserNameRow, 1).
				WillReturnRows(row(tt.enabled))

			enabled := tt.enabled
			resp, err := svc.UpdateUserSettings(ctx, &pb.UpdateUserSettingsRequest{
				UserId:    testUserNameRow,
				AiEnabled: &enabled,
			})
			if err != nil {
				t.Fatalf("UpdateUserSettings: %v", err)
			}
			if resp.User.GetAiEnabled() != tt.enabled {
				t.Errorf("AiEnabled = %v, want %v", resp.User.GetAiEnabled(), tt.enabled)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled expectations: %v", err)
			}
		})
	}
}

func TestProfileImageKeyOwner(t *testing.T) {
	tests := []struct {
		key    string
		want   string
		wantOK bool
	}{
		{"profiles/u1/a.png", "u1", true},
		{"profiles/u1/", "u1", true},
		{"profiles//a.png", "", false},
		{"profiles/u1", "", false},
		{"notes/u1/a.png", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, ok := ProfileImageKeyOwner(tt.key)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("owner (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantOK, ok); diff != "" {
				t.Errorf("ok (-want +got):\n%s", diff)
			}
		})
	}
}
