package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
)

func TestAIEligible(t *testing.T) {
	tests := []struct {
		name string
		user *User
		want bool
	}{
		{name: "nil user", user: nil, want: false},
		{name: "active opted in", user: &User{SubscriptionStatus: "active", AIEnabled: true}, want: true},
		{name: "trialing opted in", user: &User{SubscriptionStatus: "trialing", AIEnabled: true}, want: true},
		{name: "past_due opted in", user: &User{SubscriptionStatus: "past_due", AIEnabled: true}, want: true},
		{name: "active not opted in", user: &User{SubscriptionStatus: "active"}, want: false},
		{name: "free opted in", user: &User{SubscriptionStatus: "free", AIEnabled: true}, want: false},
		{name: "canceled opted in", user: &User{SubscriptionStatus: "canceled", AIEnabled: true}, want: false},
		{name: "disabled account", user: &User{SubscriptionStatus: "active", AIEnabled: true, Disabled: true}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AIEligible(tt.user); got != tt.want {
				t.Errorf("AIEligible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConsumeAIQuota(t *testing.T) {
	now := time.Date(2026, 10, 13, 23, 30, 0, 0, time.FixedZone("x", -7*3600))
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		limit   int
		setup   func(mock sqlmock.Sqlmock)
		want    bool
		wantErr bool
	}{
		{
			name:  "under cap",
			limit: 200,
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO "AIUsage" \(.+\) SELECT "id", \$1, 1 FROM "User" WHERE "id" = \$2 AND "aiEnabled" AND NOT "disabled" AND "subscriptionStatus" IN \(\$3,\$4,\$5\) ON CONFLICT (.+) DO UPDATE SET "count" = "AIUsage"."count" \+ 1 WHERE "AIUsage"."count" < \$6 RETURNING "count"`).
					WithArgs(day, testUserID, "active", "trialing", "past_due", 200).
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
			},
			want: true,
		},
		{
			name:  "at cap",
			limit: 200,
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO "AIUsage"`).
					WithArgs(day, testUserID, "active", "trialing", "past_due", 200).
					WillReturnRows(sqlmock.NewRows([]string{"count"}))
			},
			want: false,
		},
		{
			name:  "zero limit skips query",
			limit: 0,
			setup: func(sqlmock.Sqlmock) {},
			want:  false,
		},
		{
			name:  "db error",
			limit: 200,
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO "AIUsage"`).
					WillReturnError(errors.New("boom"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock.New: %v", err)
			}
			defer func() { _ = sqlDB.Close() }()
			d, err := NewFromConn(sqlDB)
			if err != nil {
				t.Fatalf("NewFromConn: %v", err)
			}
			tt.setup(mock)

			got, err := d.ConsumeAIQuota(context.Background(), testUserID, now, tt.limit)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ConsumeAIQuota() err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ConsumeAIQuota() = %v, want %v", got, tt.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestListAIEligibleUsers(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	d, err := NewFromConn(sqlDB)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}

	mock.ExpectQuery(`SELECT \* FROM "User" WHERE "aiEnabled" = \$1 AND "disabled" = \$2 AND "subscriptionStatus" IN \(\$3,\$4,\$5\)`).
		WithArgs(true, false, "active", "trialing", "past_due").
		WillReturnRows(sqlmock.NewRows([]string{"id", colSubscriptionStatus, "aiEnabled"}).
			AddRow("u1", "active", true).
			AddRow("u2", "past_due", true))

	users, err := d.ListAIEligibleUsers(context.Background())
	if err != nil {
		t.Fatalf("ListAIEligibleUsers: %v", err)
	}
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	if diff := cmp.Diff([]string{"u1", "u2"}, ids); diff != "" {
		t.Errorf("ids mismatch (-want +got):\n%s", diff)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled mock expectations: %v", err)
	}
}

func TestGetUserMediaWithoutAIText(t *testing.T) {
	tests := []struct {
		name  string
		query string
		run   func(d *DB) ([]string, error)
	}{
		{
			name:  "images",
			query: `SELECT "NoteImage"\.\* FROM "NoteImage" JOIN "Note" ON "Note"\."id" = "NoteImage"\."noteId" WHERE "Note"\."userId" = \$1 AND "NoteImage"\."extractedText" = \$2`,
			run: func(d *DB) ([]string, error) {
				imgs, err := d.GetImagesWithoutExtractedTextForUser(context.Background(), testUserID)
				ids := make([]string, 0, len(imgs))
				for _, i := range imgs {
					ids = append(ids, i.ID)
				}
				return ids, err
			},
		},
		{
			name:  "audios",
			query: `SELECT "NoteAudio"\.\* FROM "NoteAudio" JOIN "Note" ON "Note"\."id" = "NoteAudio"\."noteId" WHERE "Note"\."userId" = \$1 AND "NoteAudio"\."transcribedText" = \$2`,
			run: func(d *DB) ([]string, error) {
				auds, err := d.GetAudiosWithoutTranscriptionForUser(context.Background(), testUserID)
				ids := make([]string, 0, len(auds))
				for _, a := range auds {
					ids = append(ids, a.ID)
				}
				return ids, err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock.New: %v", err)
			}
			defer func() { _ = sqlDB.Close() }()
			d, err := NewFromConn(sqlDB)
			if err != nil {
				t.Fatalf("NewFromConn: %v", err)
			}
			mock.ExpectQuery(tt.query).
				WithArgs(testUserID, "").
				WillReturnRows(sqlmock.NewRows([]string{"id", colNoteID}).AddRow("m1", testNoteID))

			ids, err := tt.run(d)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if diff := cmp.Diff([]string{"m1"}, ids); diff != "" {
				t.Errorf("ids mismatch (-want +got):\n%s", diff)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}
