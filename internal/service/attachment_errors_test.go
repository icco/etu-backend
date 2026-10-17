package service

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/db"
	"go.icco.me/etu-backend/internal/storage"
	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func attachmentDB(t *testing.T) (*db.DB, sqlmock.Sqlmock) {
	t.Helper()
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.NewFromConn(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		mock.ExpectClose()
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	return database, mock
}

func TestAttachmentPreflight(t *testing.T) {
	for _, update := range []bool{false, true} {
		for _, tt := range []struct {
			name    string
			images  []*pb.ImageUpload
			audios  []*pb.AudioUpload
			storage *storage.Client
			want    codes.Code
		}{
			{"missing image storage", images(1), nil, nil, codes.FailedPrecondition},
			{"missing audio storage", nil, audios(1), nil, codes.FailedPrecondition},
			{"invalid later image", append(images(1), &pb.ImageUpload{Data: []byte("x"), MimeType: "text/plain"}), nil, &storage.Client{}, codes.InvalidArgument},
			{"invalid audio", nil, []*pb.AudioUpload{{Data: []byte("x"), MimeType: "text/plain"}}, &storage.Client{}, codes.InvalidArgument},
			{"nil image", []*pb.ImageUpload{nil}, nil, &storage.Client{}, codes.InvalidArgument},
			{"nil audio", nil, []*pb.AudioUpload{nil}, &storage.Client{}, codes.InvalidArgument},
		} {
			name := "create/" + tt.name
			if update {
				name = "update/" + tt.name
			}
			t.Run(name, func(t *testing.T) {
				database, _ := attachmentDB(t)
				svc := NewNotesService(database, tt.storage, nil, "")
				ctx := auth.SetAuthContext(context.Background(), "alice", "apikey")
				var err error
				if update {
					_, err = svc.UpdateNote(ctx, &pb.UpdateNoteRequest{UserId: "alice", Id: "saved-note", AddImages: tt.images, AddAudios: tt.audios})
				} else {
					_, err = svc.CreateNote(ctx, &pb.CreateNoteRequest{UserId: "alice", Content: "keep this text", Images: tt.images, Audios: tt.audios})
				}
				if status.Code(err) != tt.want {
					t.Fatalf("code = %v, want %v: %v", status.Code(err), tt.want, err)
				}
			})
		}
	}
}

type attachmentStorage struct {
	uploads    []string
	deleted    []string
	failUpload int
	deleteErr  error
}

func (s *attachmentStorage) UploadImage(_ context.Context, object string, _ []byte, _ string) (string, error) {
	s.uploads = append(s.uploads, object)
	if len(s.uploads) == s.failUpload {
		return "", errors.New("private storage detail")
	}
	return "https://example.com/media", nil
}

func (s *attachmentStorage) DeleteImage(_ context.Context, object string) error {
	s.deleted = append(s.deleted, object)
	return s.deleteErr
}

func (*attachmentStorage) GetSignedURL(context.Context, string) (string, error) {
	return "https://example.com/media", nil
}

type savedNoteID struct{ value string }

func (id *savedNoteID) Match(value driver.Value) bool {
	s, ok := value.(string)
	if !ok || s == "" {
		return false
	}
	id.value = s
	return true
}

func expectSavedNote(mock sqlmock.Sqlmock, update bool, id *savedNoteID) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM "User"`).WithArgs("alice").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("alice"))
	if update {
		id.value = "saved-note"
		mock.ExpectQuery(`SELECT \* FROM "Note"`).WillReturnRows(sqlmock.NewRows([]string{"id", "userId", "content", "createdAt", "updatedAt"}).AddRow(id.value, "alice", "keep this text", time.Now(), time.Now()))
		mock.ExpectExec(`UPDATE "Note"`).WillReturnResult(sqlmock.NewResult(0, 1))
	} else {
		mock.ExpectExec(`INSERT INTO "Note"`).WithArgs(id, "keep this text", sqlmock.AnyArg(), sqlmock.AnyArg(), "alice", nil, nil, nil).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()
	mock.ExpectQuery(`FROM "Tag"`).WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	mock.ExpectQuery(`FROM "NoteImage"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
}

func TestAttachmentPartialFailure(t *testing.T) {
	for _, update := range []bool{false, true} {
		for _, kind := range []string{"image", "audio"} {
			for _, failure := range []string{"upload", "database"} {
				name := "create/" + kind + "/" + failure
				if update {
					name = "update/" + kind + "/" + failure
				}
				t.Run(name, func(t *testing.T) {
					database, mock := attachmentDB(t)
					id := &savedNoteID{}
					expectSavedNote(mock, update, id)
					store := &attachmentStorage{failUpload: 2}
					count := 1
					if failure == "database" {
						store.failUpload = 0
						count = 2
						store.deleteErr = errors.New("cleanup failed")
					}
					for i := range count {
						mock.ExpectBegin()
						mock.ExpectQuery(`SELECT "User".id FROM "User" JOIN "Note"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("alice"))
						table := "NoteImage"
						if kind == "audio" {
							table = "NoteAudio"
						}
						insert := mock.ExpectExec(`INSERT INTO "` + table + `"`)
						if i == 1 {
							insert.WillReturnError(errors.New("private database detail"))
							mock.ExpectRollback()
						} else {
							insert.WillReturnResult(sqlmock.NewResult(0, 1))
							mock.ExpectCommit()
						}
					}
					svc := NewNotesService(database, nil, nil, "")
					svc.storage = store
					ctx := auth.SetAuthContext(context.Background(), "alice", "apikey")
					var img []*pb.ImageUpload
					var aud []*pb.AudioUpload
					if kind == "image" {
						img = images(3)
					} else {
						aud = audios(2)
					}
					var err error
					if update {
						_, err = svc.UpdateNote(ctx, &pb.UpdateNoteRequest{UserId: "alice", Id: "saved-note", AddImages: img, AddAudios: aud})
					} else {
						_, err = svc.CreateNote(ctx, &pb.CreateNoteRequest{UserId: "alice", Content: "keep this text", Images: img, Audios: aud})
					}
					if status.Code(err) != codes.Internal {
						t.Fatalf("code = %v: %v", status.Code(err), err)
					}
					st := status.Convert(err)
					if !strings.Contains(st.Message(), id.value) || strings.Contains(st.Message(), "private") {
						t.Fatalf("unsafe or unrecoverable message: %v", st.Message())
					}
					details := st.Details()
					if len(details) != 1 {
						t.Fatalf("details: %v", details)
					}
					info, ok := details[0].(*errdetails.ErrorInfo)
					if !ok {
						t.Fatalf("unexpected detail: %T", details[0])
					}
					want := &errdetails.ErrorInfo{Reason: "ATTACHMENT_UPLOAD_FAILED", Domain: "etu", Metadata: map[string]string{"note_id": id.value, "attachment_type": kind, "attachment_index": "1"}}
					if diff := cmp.Diff(want.GetMetadata(), info.GetMetadata()); diff != "" {
						t.Error(diff)
					}
					if info.Reason != want.Reason || info.Domain != want.Domain {
						t.Errorf("error detail: %v", info)
					}
					if len(store.uploads) != 2 {
						t.Errorf("uploads continued after failure: %v", store.uploads)
					}
					if failure == "database" {
						if diff := cmp.Diff([]string{store.uploads[1]}, store.deleted); diff != "" {
							t.Errorf("cleanup (-want +got): %s", diff)
						}
					} else if len(store.deleted) != 0 {
						t.Errorf("deleted saved uploads: %v", store.deleted)
					}
				})
			}
		}
	}
}
