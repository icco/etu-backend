package db

import (
	"context"
	"errors"
	"fmt"
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
		{name: "trial opted in", user: &User{SubscriptionStatus: "trial", AIEnabled: true}, want: true},
		{name: "pro opted in", user: &User{SubscriptionStatus: "pro", AIEnabled: true}, want: true},
		{name: "premium opted in", user: &User{SubscriptionStatus: "premium", AIEnabled: true}, want: true},
		{name: "uppercase active", user: &User{SubscriptionStatus: "Active", AIEnabled: true}, want: false},
		{name: "uppercase pro", user: &User{SubscriptionStatus: "PRO", AIEnabled: true}, want: false},
		{name: "empty status", user: &User{SubscriptionStatus: "", AIEnabled: true}, want: false},
		{name: "incomplete opted in", user: &User{SubscriptionStatus: "incomplete", AIEnabled: true}, want: false},
		{name: "unpaid opted in", user: &User{SubscriptionStatus: "unpaid", AIEnabled: true}, want: false},
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
				mock.ExpectQuery(`INSERT INTO "AIUsage" \(.+\) SELECT "id", \$1, 1 FROM "User" WHERE "id" = \$2 AND "aiEnabled" AND NOT "disabled" AND "subscriptionStatus" IN \(\$3,\$4,\$5,\$6,\$7,\$8\) ON CONFLICT (.+) DO UPDATE SET "count" = "AIUsage"."count" \+ 1 WHERE "AIUsage"."count" < \$9 RETURNING "count"`).
					WithArgs(day, testUserID, "active", "trial", "trialing", "pro", "premium", "past_due", 200).
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
			},
			want: true,
		},
		{
			name:  "at cap",
			limit: 200,
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO "AIUsage"`).
					WithArgs(day, testUserID, "active", "trial", "trialing", "pro", "premium", "past_due", 200).
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

func TestListPendingAIMedia(t *testing.T) {
	eligible := `JOIN "Note" ON "Note"\."id" = "%[1]s"\."noteId" JOIN "User" ON "User"\."id" = "Note"\."userId" WHERE "%[1]s"\."%[2]s" = \$1 AND "User"\."aiEnabled" AND NOT "User"\."disabled" AND "User"\."subscriptionStatus" IN \(\$2,\$3,\$4,\$5,\$6,\$7\) ORDER BY "Note"\."userId"`
	tests := []struct {
		name  string
		query string
		run   func(d *DB) ([][2]string, error)
	}{
		{
			name:  "images",
			query: `SELECT "NoteImage"\.\*, "Note"\."userId" AS "ownerId" FROM "NoteImage" ` + fmt.Sprintf(eligible, "NoteImage", "extractedText"),
			run: func(d *DB) ([][2]string, error) {
				rows, err := d.ListPendingAIImages(context.Background())
				out := make([][2]string, 0, len(rows))
				for _, r := range rows {
					out = append(out, [2]string{r.ID, r.OwnerID})
				}
				return out, err
			},
		},
		{
			name:  "audios",
			query: `SELECT "NoteAudio"\.\*, "Note"\."userId" AS "ownerId" FROM "NoteAudio" ` + fmt.Sprintf(eligible, "NoteAudio", "transcribedText"),
			run: func(d *DB) ([][2]string, error) {
				rows, err := d.ListPendingAIAudios(context.Background())
				out := make([][2]string, 0, len(rows))
				for _, r := range rows {
					out = append(out, [2]string{r.ID, r.OwnerID})
				}
				return out, err
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
				WithArgs("", "active", "trial", "trialing", "pro", "premium", "past_due").
				WillReturnRows(sqlmock.NewRows([]string{"id", colNoteID, "ownerId"}).
					AddRow("m1", testNoteID, testUserID).
					AddRow("m2", testNoteID, testUserID))

			got, err := tt.run(d)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			want := [][2]string{{"m1", testUserID}, {"m2", testUserID}}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("rows mismatch (-want +got):\n%s", diff)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}
