package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/billing"
	"go.icco.me/etu-backend/internal/db"
	pb "go.icco.me/etu-backend/proto"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeAccountStorage struct {
	calls   []string
	failDel bool
}

func (f *fakeAccountStorage) DeleteImage(_ context.Context, name string) error {
	f.calls = append(f.calls, "delete "+name)
	if f.failDel {
		return errors.New("gcs down")
	}
	return nil
}

func (f *fakeAccountStorage) DeletePrefix(_ context.Context, prefix string) error {
	f.calls = append(f.calls, "prefix "+prefix)
	return nil
}

func (f *fakeAccountStorage) GetSignedURLWithExpiry(_ context.Context, name string, ttl time.Duration) (string, error) {
	return "https://signed/" + name + "?ttl=" + ttl.String(), nil
}

type fakeCanceler struct {
	calls []string
	err   error
}

func (f *fakeCanceler) CancelSubscriptions(_ context.Context, customerID, subscriptionID string) error {
	f.calls = append(f.calls, customerID+"/"+subscriptionID)
	return f.err
}

var accountUserColumns = []string{
	"id", colEmail, colPasswordHash, colFailedLoginAttempts,
	"stripeCustomerId", "stripeSubscriptionId", "profileImageGCSObject", colCreatedAt, colUpdatedAt, colDisabled, colSubscriptionStatus,
}

const (
	acctUser  = "alice"
	acctOther = "bob"
)

func TestDeleteAccount(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	cus, sub, avatar := "cus_1", "sub_1", "profiles/alice/avatar-1"
	apiKeyCtx := auth.SetAuthContext(context.Background(), acctUser, "apikey")
	m2mCtx := auth.SetAuthContext(context.Background(), "m2m", "m2m")

	tests := []struct {
		name         string
		ctx          context.Context
		req          *pb.DeleteAccountRequest
		userExists   bool
		billingErr   error
		noBilling    bool
		storageFail  bool
		expectDelete bool
		expectFailed bool
		expectFreeze bool
		freezeFail   bool
		disabled     bool
		wantMsg      string
		cleanup      []string
		cleanupDone  int
		subStatus    string
		noStripeIDs  bool
		wantCode     codes.Code
		wantBilling  []string
		wantStorage  []string
	}{
		{
			name:         "owner with password deletes everything",
			ctx:          apiKeyCtx,
			req:          &pb.DeleteAccountRequest{UserId: acctUser, Password: testPassword},
			userExists:   true,
			expectDelete: true,
			expectFreeze: true,
			wantBilling:  []string{"cus_1/sub_1"},
			wantStorage:  []string{"delete notes/n1/i1", "delete " + avatar, "prefix profiles/alice/", "delete notes/n2/late", "prefix profiles/alice/"},
		},
		{
			name:         "m2m skips password",
			ctx:          m2mCtx,
			req:          &pb.DeleteAccountRequest{UserId: acctUser},
			userExists:   true,
			expectDelete: true,
			expectFreeze: true,
			wantBilling:  []string{"cus_1/sub_1"},
			wantStorage:  []string{"delete notes/n1/i1", "delete " + avatar, "prefix profiles/alice/", "delete notes/n2/late", "prefix profiles/alice/"},
		},
		{
			name:     "other user is denied",
			ctx:      apiKeyCtx,
			req:      &pb.DeleteAccountRequest{UserId: acctOther, Password: testPassword},
			wantCode: codes.PermissionDenied,
		},
		{
			name:       "missing password",
			ctx:        apiKeyCtx,
			req:        &pb.DeleteAccountRequest{UserId: acctUser},
			userExists: true,
			wantCode:   codes.InvalidArgument,
		},
		{
			name:         "wrong password records failure",
			ctx:          apiKeyCtx,
			req:          &pb.DeleteAccountRequest{UserId: acctUser, Password: "nope"},
			userExists:   true,
			expectFailed: true,
			wantCode:     codes.PermissionDenied,
		},
		{
			name:         "stripe failure aborts before deleting",
			ctx:          apiKeyCtx,
			req:          &pb.DeleteAccountRequest{UserId: acctUser, Password: testPassword},
			userExists:   true,
			billingErr:   errors.New("stripe down"),
			expectFreeze: true,
			wantMsg:      "contact support",
			wantCode:     codes.Unavailable,
			wantBilling:  []string{"cus_1/sub_1"},
		},
		{
			name:       "subscribed user without billing configured aborts",
			ctx:        apiKeyCtx,
			req:        &pb.DeleteAccountRequest{UserId: acctUser, Password: testPassword},
			userExists: true,
			noBilling:  true,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:         "gcs failure aborts before db delete",
			ctx:          apiKeyCtx,
			req:          &pb.DeleteAccountRequest{UserId: acctUser, Password: testPassword},
			userExists:   true,
			storageFail:  true,
			expectFreeze: true,
			wantMsg:      "contact support",
			wantCode:     codes.Unavailable,
			wantBilling:  []string{"cus_1/sub_1"},
			wantStorage:  []string{"delete notes/n1/i1"},
		},
		{
			name:         "m2m retry completes a frozen account",
			ctx:          m2mCtx,
			req:          &pb.DeleteAccountRequest{UserId: acctUser},
			userExists:   true,
			disabled:     true,
			expectFreeze: true,
			expectDelete: true,
			wantBilling:  []string{"cus_1/sub_1"},
			wantStorage:  []string{"delete notes/n1/i1", "delete " + avatar, "prefix profiles/alice/", "delete notes/n2/late", "prefix profiles/alice/"},
		},
		{
			name:         "freeze failure aborts before stripe",
			ctx:          apiKeyCtx,
			req:          &pb.DeleteAccountRequest{UserId: acctUser, Password: testPassword},
			userExists:   true,
			expectFreeze: true,
			freezeFail:   true,
			wantCode:     codes.Internal,
		},
		{
			name:        "paid status without stripe ids needs support",
			ctx:         m2mCtx,
			req:         &pb.DeleteAccountRequest{UserId: acctUser},
			userExists:  true,
			subStatus:   "trialing",
			noStripeIDs: true,
			wantCode:    codes.FailedPrecondition,
			wantMsg:     "contact support",
		},
		{
			name:        "etu-web trial status without stripe ids needs support",
			ctx:         m2mCtx,
			req:         &pb.DeleteAccountRequest{UserId: acctUser},
			userExists:  true,
			subStatus:   "trial",
			noStripeIDs: true,
			wantCode:    codes.FailedPrecondition,
			wantMsg:     "contact support",
		},
		{
			name:         "manual pro grant without stripe ids is deletable",
			ctx:          m2mCtx,
			req:          &pb.DeleteAccountRequest{UserId: acctUser},
			userExists:   true,
			subStatus:    "pro",
			noStripeIDs:  true,
			expectFreeze: true,
			expectDelete: true,
			wantStorage:  []string{"delete notes/n1/i1", "delete " + avatar, "prefix profiles/alice/", "delete notes/n2/late", "prefix profiles/alice/"},
		},
		{
			name:         "manual premium grant without stripe ids is deletable",
			ctx:          m2mCtx,
			req:          &pb.DeleteAccountRequest{UserId: acctUser},
			userExists:   true,
			subStatus:    "premium",
			noStripeIDs:  true,
			expectFreeze: true,
			expectDelete: true,
			wantStorage:  []string{"delete notes/n1/i1", "delete " + avatar, "prefix profiles/alice/", "delete notes/n2/late", "prefix profiles/alice/"},
		},
		{
			name:        "past_due status without stripe ids needs support",
			ctx:         m2mCtx,
			req:         &pb.DeleteAccountRequest{UserId: acctUser},
			userExists:  true,
			subStatus:   "past_due",
			noStripeIDs: true,
			wantCode:    codes.FailedPrecondition,
			wantMsg:     "contact support",
		},
		{
			name:        "m2m retry finishes recorded media cleanup",
			ctx:         m2mCtx,
			req:         &pb.DeleteAccountRequest{UserId: acctUser},
			cleanup:     []string{"notes/n2/late", "profiles/alice/"},
			cleanupDone: 2,
			wantStorage: []string{"delete notes/n2/late", "prefix profiles/alice/"},
		},
		{
			name:        "failed cleanup on retry keeps records and errors",
			ctx:         m2mCtx,
			req:         &pb.DeleteAccountRequest{UserId: acctUser},
			cleanup:     []string{"notes/n2/late"},
			storageFail: true,
			wantCode:    codes.Unavailable,
			wantStorage: []string{"delete notes/n2/late"},
		},
		{
			name: "already deleted user is success for retries",
			ctx:  m2mCtx,
			req:  &pb.DeleteAccountRequest{UserId: acctUser},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sqlDB.Close() }()
			database, err := db.NewFromConn(sqlDB)
			if err != nil {
				t.Fatal(err)
			}
			store := &fakeAccountStorage{failDel: tt.storageFail}
			canceler := &fakeCanceler{err: tt.billingErr}
			svc := &AccountService{db: database, storage: store, billing: canceler}
			if tt.noBilling {
				svc.billing = nil
			}

			if tt.wantCode != codes.PermissionDenied || tt.expectFailed {
				rows := sqlmock.NewRows(accountUserColumns)
				if tt.userExists {
					var c, s any = cus, sub
					if tt.noStripeIDs {
						c, s = nil, nil
					}
					rows.AddRow(acctUser, testEmail, string(hash), 0, c, s, avatar, time.Now(), time.Now(), tt.disabled, tt.subStatus)
				}
				mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs(acctUser, 1).WillReturnRows(rows)
				if !tt.userExists {
					expectCleanup(mock, tt.cleanup, tt.cleanupDone)
				}
			}
			if tt.expectFailed {
				mock.ExpectBegin()
				mock.ExpectExec(`UPDATE "User" SET "failedLoginAttempts"`).WithArgs(sqlmock.AnyArg(), acctUser).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			if tt.userExists && !tt.expectFailed && tt.wantCode != codes.InvalidArgument {
				mock.ExpectQuery(activePlayQ).WithArgs(acctUser, stateExpired, statePendingCanceled).
					WillReturnRows(sqlmock.NewRows([]string{"purchaseToken"}))
			}
			if tt.expectFreeze {
				mock.ExpectBegin()
				mock.ExpectExec(`UPDATE "User" SET disabled = true`).WithArgs(db.DeletionDisabledReason, acctUser).WillReturnResult(sqlmock.NewResult(0, 1))
				revoke := mock.ExpectExec(`DELETE FROM "ApiKey"`).WithArgs(acctUser)
				if tt.freezeFail {
					revoke.WillReturnError(errors.New("db down"))
					mock.ExpectRollback()
				} else {
					revoke.WillReturnResult(sqlmock.NewResult(0, 2))
					mock.ExpectCommit()
				}
			}
			if tt.expectFreeze && !tt.freezeFail && tt.billingErr == nil {
				mock.ExpectQuery(activePlayQ).WithArgs(acctUser, stateExpired, statePendingCanceled).
					WillReturnRows(sqlmock.NewRows([]string{"purchaseToken"}))
			}
			if tt.expectFreeze && len(tt.wantStorage) > 0 {
				mock.ExpectQuery(`SELECT "NoteImage"."gcsObjectName"`).WithArgs(acctUser, acctUser).
					WillReturnRows(sqlmock.NewRows([]string{"gcsObjectName"}).AddRow("notes/n1/i1"))
			}
			if tt.expectDelete {
				mock.ExpectBegin()
				mock.ExpectQuery(`FOR UPDATE`).WithArgs(acctUser).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(acctUser))
				mock.ExpectQuery(`SELECT "NoteImage"."gcsObjectName"`).WithArgs(acctUser, acctUser).
					WillReturnRows(sqlmock.NewRows([]string{"gcsObjectName"}).AddRow("notes/n1/i1").AddRow("notes/n2/late"))
				mock.ExpectExec(`INSERT INTO "AccountDeletionCleanup"`).WillReturnResult(sqlmock.NewResult(0, 2))
				for range 10 {
					mock.ExpectExec(`DELETE FROM`).WithArgs(acctUser).WillReturnResult(sqlmock.NewResult(0, 1))
				}
				mock.ExpectCommit()
				expectCleanup(mock, []string{"notes/n2/late", "profiles/alice/"}, 2)
			}

			resp, err := svc.DeleteAccount(tt.ctx, tt.req)
			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("code = %v, want %v (err %v)", got, tt.wantCode, err)
			}
			if tt.wantMsg != "" && !strings.Contains(status.Convert(err).Message(), tt.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", status.Convert(err).Message(), tt.wantMsg)
			}
			if err == nil && !resp.Success {
				t.Error("expected success")
			}
			if diff := cmp.Diff(tt.wantBilling, canceler.calls); diff != "" {
				t.Errorf("billing calls (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantStorage, store.calls); diff != "" {
				t.Errorf("storage calls (-want +got):\n%s", diff)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled expectations: %v", err)
			}
		})
	}
}

func TestDeleteAccountCancelsPlay(t *testing.T) {
	m2mCtx := auth.SetAuthContext(context.Background(), "m2m", "m2m")
	tests := []struct {
		name        string
		playState   string
		noPlay      bool
		cancelErr   error
		wantCancels []string
		wantCode    codes.Code
		wantFreeze  bool
		wantDelete  bool
		lateOnly    bool
	}{
		{name: "active play subscription is canceled", playState: stateActive, wantCancels: []string{playTok}, wantFreeze: true, wantDelete: true},
		{name: "canceled play subscription is not canceled again", playState: stateCancel, wantFreeze: true, wantDelete: true},
		{name: "purchase unknown to play is skipped", wantFreeze: true, wantDelete: true},
		{name: "play cancel failure aborts", playState: stateActive, cancelErr: errors.New("play down"), wantCancels: []string{playTok}, wantFreeze: true, wantCode: codes.Unavailable},
		{name: "play not configured aborts", playState: stateActive, noPlay: true, wantCode: codes.FailedPrecondition},
		{name: "purchase recorded during freeze is canceled", playState: stateActive, lateOnly: true, wantCancels: []string{playTok}, wantFreeze: true, wantDelete: true},
		{name: "purchase recorded during freeze without play configured aborts", playState: stateActive, lateOnly: true, noPlay: true, wantFreeze: true, wantCode: codes.FailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sqlDB.Close() }()
			database, err := db.NewFromConn(sqlDB)
			if err != nil {
				t.Fatal(err)
			}
			purchases := map[string]*billing.PlayPurchase{}
			if tt.playState != "" {
				purchases[playTok] = alicePurchase(tt.playState)
			}
			play := &fakePlay{purchases: purchases, cancelErr: tt.cancelErr}
			canceler := &fakeCanceler{}
			svc := NewAccountService(database, nil, canceler)
			svc.storage = &fakeAccountStorage{}
			if !tt.noPlay {
				svc.SetPlay(play)
			}

			now := time.Now()
			subStatus := "trialing"
			if tt.lateOnly {
				subStatus = "canceled"
			}
			mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs(acctUser, 1).WillReturnRows(sqlmock.NewRows(accountUserColumns).
				AddRow(acctUser, testEmail, "h", 0, nil, nil, nil, now, now, false, subStatus))
			playRows := func(withRow bool) *sqlmock.Rows {
				rows := sqlmock.NewRows([]string{"purchaseToken", "userId", "productId", "basePlanId", "state", colCreatedAt, colUpdatedAt})
				if withRow {
					rows.AddRow(playTok, acctUser, playProduct, "yearly", stateActive, now, now)
				}
				return rows
			}
			mock.ExpectQuery(activePlayQ).WithArgs(acctUser, stateExpired, statePendingCanceled).WillReturnRows(playRows(!tt.lateOnly))
			if tt.wantFreeze {
				mock.ExpectBegin()
				mock.ExpectExec(`UPDATE "User" SET disabled = true`).WithArgs(db.DeletionDisabledReason, acctUser).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(`DELETE FROM "ApiKey"`).WithArgs(acctUser).WillReturnResult(sqlmock.NewResult(0, 2))
				mock.ExpectCommit()
				mock.ExpectQuery(activePlayQ).WithArgs(acctUser, stateExpired, statePendingCanceled).WillReturnRows(playRows(true))
			}
			if tt.wantDelete {
				mock.ExpectQuery(`SELECT "NoteImage"."gcsObjectName"`).WithArgs(acctUser, acctUser).
					WillReturnRows(sqlmock.NewRows([]string{"gcsObjectName"}))
				mock.ExpectBegin()
				mock.ExpectQuery(`FOR UPDATE`).WithArgs(acctUser).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(acctUser))
				mock.ExpectQuery(`SELECT "NoteImage"."gcsObjectName"`).WithArgs(acctUser, acctUser).
					WillReturnRows(sqlmock.NewRows([]string{"gcsObjectName"}))
				mock.ExpectExec(`INSERT INTO "AccountDeletionCleanup"`).WillReturnResult(sqlmock.NewResult(0, 1))
				for range 10 {
					mock.ExpectExec(`DELETE FROM`).WithArgs(acctUser).WillReturnResult(sqlmock.NewResult(0, 1))
				}
				mock.ExpectCommit()
				expectCleanup(mock, []string{"profiles/alice/"}, 1)
			}

			_, err = svc.DeleteAccount(m2mCtx, &pb.DeleteAccountRequest{UserId: acctUser})
			if diff := cmp.Diff(tt.wantCode, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s (err=%v)", diff, err)
			}
			if diff := cmp.Diff(tt.wantCancels, play.cancels); diff != "" {
				t.Errorf("play cancels mismatch (-want +got):\n%s", diff)
			}
			if len(canceler.calls) != 0 {
				t.Errorf("stripe calls = %v, want none", canceler.calls)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled expectations: %v", err)
			}
		})
	}
}

type fakeExportStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent []*pb.ExportDataResponse
}

func (f *fakeExportStream) Context() context.Context { return f.ctx }

func (f *fakeExportStream) Send(r *pb.ExportDataResponse) error {
	f.sent = append(f.sent, r)
	return nil
}

func TestExportData(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		userID   string
		wantCode codes.Code
	}{
		{name: "other user denied", ctx: auth.SetAuthContext(context.Background(), acctUser, "apikey"), userID: acctOther, wantCode: codes.PermissionDenied},
		{name: "missing user id", ctx: auth.SetAuthContext(context.Background(), acctUser, "apikey"), wantCode: codes.InvalidArgument},
		{name: "unauthenticated", ctx: context.Background(), userID: acctUser, wantCode: codes.Unauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &AccountService{}
			err := svc.ExportData(&pb.ExportDataRequest{UserId: tt.userID}, &fakeExportStream{ctx: tt.ctx})
			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("code = %v, want %v", got, tt.wantCode)
			}
		})
	}
}

func TestExportDataPaginatesAllNotes(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	database, err := db.NewFromConn(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	svc := &AccountService{db: database, storage: &fakeAccountStorage{}}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT \* FROM "User"`).WithArgs(acctUser, 1).
		WillReturnRows(sqlmock.NewRows(accountUserColumns).AddRow(acctUser, testEmail, "h", 0, nil, nil, nil, base, base, false, ""))
	mock.ExpectQuery(`FROM "Tag"`).WithArgs(acctUser).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "count", colCreatedAt}).AddRow("t1", "work", 1, base))

	total := exportPageSize + 1
	noteCols := []string{"id", "content", colCreatedAt, colUpdatedAt, "userId"}
	page1 := sqlmock.NewRows(noteCols)
	for i := range exportPageSize {
		page1.AddRow(noteID(i), "c", base.Add(time.Duration(i)*time.Second), base, acctUser)
	}
	mock.ExpectQuery(regexp.QuoteMeta(`ORDER BY "createdAt" ASC, id ASC LIMIT $2`)).WithArgs(acctUser, exportPageSize).WillReturnRows(page1)
	mock.ExpectQuery(`FROM "Tag"`).WillReturnRows(sqlmock.NewRows([]string{"note_id", "id", "name"}))
	mock.ExpectQuery(`FROM "NoteImage"`).WillReturnRows(sqlmock.NewRows([]string{"id", "noteId", "gcsObjectName"}).AddRow("i1", noteID(0), "notes/n0/i1"))
	mock.ExpectQuery(`FROM "NoteAudio"`).WillReturnRows(sqlmock.NewRows([]string{"id", "noteId", "gcsObjectName"}))

	lastAt := base.Add(time.Duration(exportPageSize-1) * time.Second)
	mock.ExpectQuery(regexp.QuoteMeta(`("createdAt", id) > ($2, $3)`)).WithArgs(acctUser, lastAt, noteID(exportPageSize-1), exportPageSize).
		WillReturnRows(sqlmock.NewRows(noteCols).AddRow(noteID(exportPageSize), "c", lastAt.Add(time.Second), base, acctUser))
	mock.ExpectQuery(`FROM "Tag"`).WillReturnRows(sqlmock.NewRows([]string{"note_id", "id", "name"}))
	mock.ExpectQuery(`FROM "NoteImage"`).WillReturnRows(sqlmock.NewRows([]string{"id", "noteId", "gcsObjectName"}))
	mock.ExpectQuery(`FROM "NoteAudio"`).WillReturnRows(sqlmock.NewRows([]string{"id", "noteId", "gcsObjectName"}))

	stream := &fakeExportStream{ctx: auth.SetAuthContext(context.Background(), acctUser, "apikey")}
	if err := svc.ExportData(&pb.ExportDataRequest{UserId: acctUser}, stream); err != nil {
		t.Fatalf("ExportData: %v", err)
	}

	var users, tags, notes int
	var imageURL string
	for _, r := range stream.sent {
		switch {
		case r.GetUser() != nil:
			users++
		case r.GetTag() != nil:
			tags++
		case r.GetNote() != nil:
			notes++
			if imgs := r.GetNote().Images; len(imgs) > 0 {
				imageURL = imgs[0].Url
			}
		}
	}
	if diff := cmp.Diff([]int{1, 1, total}, []int{users, tags, notes}); diff != "" {
		t.Errorf("users/tags/notes (-want +got):\n%s", diff)
	}
	if imageURL != "https://signed/notes/n0/i1?ttl=1h0m0s" {
		t.Errorf("image url = %q", imageURL)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func noteID(i int) string {
	return "n" + time.Duration(i).String()
}

func TestNoteWritesForDisabledUser(t *testing.T) {
	content := "x"
	tests := []struct {
		name string
		call func(*NotesService, context.Context) error
	}{
		{name: "create", call: func(s *NotesService, ctx context.Context) error {
			_, err := s.CreateNote(ctx, &pb.CreateNoteRequest{UserId: acctUser, Content: content})
			return err
		}},
		{name: "update", call: func(s *NotesService, ctx context.Context) error {
			_, err := s.UpdateNote(ctx, &pb.UpdateNoteRequest{UserId: acctUser, Id: "n1", Content: &content})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sqlDB.Close() }()
			database, err := db.NewFromConn(sqlDB)
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			mock.ExpectQuery(`FOR SHARE`).WithArgs(acctUser).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			mock.ExpectRollback()

			err = tt.call(NewNotesService(database, nil, nil, ""), auth.SetAuthContext(context.Background(), "m2m", "m2m"))
			if status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition (err %v)", status.Code(err), err)
			}
		})
	}
}

func expectCleanup(mock sqlmock.Sqlmock, names []string, done int) {
	rows := sqlmock.NewRows([]string{"objectName"})
	for _, n := range names {
		rows.AddRow(n)
	}
	mock.ExpectQuery(`FROM "AccountDeletionCleanup"`).WithArgs(acctUser).WillReturnRows(rows)
	for _, n := range names[:done] {
		mock.ExpectBegin()
		mock.ExpectExec(`DELETE FROM "AccountDeletionCleanup"`).WithArgs(acctUser, n).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
	}
}
