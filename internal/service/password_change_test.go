package service

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"go.icco.me/etu-backend/internal/auth"
	pb "go.icco.me/etu-backend/proto"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUpdateUserSettings_PasswordChange(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	newPassword := "n3w-passw0rd"
	ptr := func(s string) *string { return &s }
	for _, tc := range []struct {
		name     string
		current  *string
		disabled bool
		attempts int
		want     codes.Code
	}{
		{name: "missing current password", want: codes.InvalidArgument},
		{name: "wrong current password records failure", current: ptr("wrong"), want: codes.PermissionDenied},
		{name: "locked account", current: ptr(testPassword), attempts: 5, want: codes.PermissionDenied},
		{name: "disabled account", current: ptr(testPassword), disabled: true, want: codes.PermissionDenied},
		{name: "success revokes keys", current: ptr(testPassword), want: codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, mock, cleanup := newTestUserSettingsService(t, "")
			defer cleanup()
			now := time.Now()
			row := func() *sqlmock.Rows {
				return sqlmock.NewRows(userColumns).AddRow(
					"u1", testEmail, nil, string(hash), "active", nil, now, nil, nil, nil, nil, now,
					tc.disabled, nil, tc.attempts, now,
				)
			}
			if tc.current != nil {
				mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs("u1", 1).WillReturnRows(row())
			}
			switch tc.name {
			case "wrong current password records failure":
				mock.ExpectBegin()
				mock.ExpectExec(`UPDATE "User" SET "failedLoginAttempts"="failedLoginAttempts" \+ 1`).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			case "success revokes keys":
				mock.ExpectBegin()
				mock.ExpectExec(`UPDATE "User" SET`).WithArgs(0, nil, sqlmock.AnyArg(), sqlmock.AnyArg(), "u1").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(`DELETE FROM "ApiKey" WHERE "userId" = \$1`).WithArgs("u1").WillReturnResult(sqlmock.NewResult(0, 2))
				mock.ExpectCommit()
				mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs("u1", 1).WillReturnRows(row())
				mock.ExpectBegin()
				mock.ExpectExec(`UPDATE "User"`).WithArgs(sqlmock.AnyArg(), "u1").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs("u1", "u1", 1).WillReturnRows(row())
			}
			ctx := auth.SetAuthContext(context.Background(), "u1", "apikey")
			_, err := svc.UpdateUserSettings(ctx, &pb.UpdateUserSettingsRequest{UserId: "u1", Password: &newPassword, CurrentPassword: tc.current})
			if status.Code(err) != tc.want {
				t.Fatalf("code = %v (%v), want %v", status.Code(err), err, tc.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
