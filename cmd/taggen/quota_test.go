package main

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"go.icco.me/etu-backend/internal/db"
)

func TestDailyAILimit(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		want    int
		wantErr bool
	}{
		{name: "default", env: "", want: defaultDailyAILimit},
		{name: "custom", env: "50", want: 50},
		{name: "zero disables", env: "0", want: 0},
		{name: "negative", env: "-1", wantErr: true},
		{name: "garbage", env: "lots", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AI_DAILY_LIMIT", tt.env)
			got, err := dailyAILimit()
			if (err != nil) != tt.wantErr {
				t.Fatalf("dailyAILimit() err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("dailyAILimit() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAllowAI(t *testing.T) {
	tests := []struct {
		name  string
		setup func(mock sqlmock.Sqlmock)
		want  bool
	}{
		{
			name: "under cap",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO "AIUsage"`).
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			},
			want: true,
		},
		{
			name: "cap reached",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO "AIUsage"`).
					WillReturnRows(sqlmock.NewRows([]string{"count"}))
			},
			want: false,
		},
		{
			name: "db error fails closed",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO "AIUsage"`).
					WillReturnError(errors.New("boom"))
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock.New: %v", err)
			}
			defer func() { _ = sqlDB.Close() }()
			database, err := db.NewFromConn(sqlDB)
			if err != nil {
				t.Fatalf("NewFromConn: %v", err)
			}
			tt.setup(mock)

			if got := allowAI(context.Background(), database, "alice", 200); got != tt.want {
				t.Errorf("allowAI() = %v, want %v", got, tt.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}
