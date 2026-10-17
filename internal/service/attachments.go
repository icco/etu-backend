package service

import (
	"context"
	"fmt"
	"strconv"

	"go.icco.me/etu-backend/internal/db"
	pb "go.icco.me/etu-backend/proto"
	"go.icco.me/gutil/logging"
	"go.uber.org/zap"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type noteStorage interface {
	UploadImage(context.Context, string, []byte, string) (string, error)
	DeleteImage(context.Context, string) error
	GetSignedURL(context.Context, string) (string, error)
}

// validateAttachments rejects bad input before any write, so a failed request leaves nothing behind.
func (s *NotesService) validateAttachments(images []*pb.ImageUpload, audios []*pb.AudioUpload) error {
	if err := checkAttachmentCounts(len(images), len(audios)); err != nil {
		return err
	}
	if (len(images) > 0 || len(audios) > 0) && s.storage == nil {
		return status.Error(codes.FailedPrecondition, "storage is not configured")
	}
	for i, img := range images {
		if img == nil {
			return status.Errorf(codes.InvalidArgument, "image %d is empty", i)
		}
		if err := validateImage(img.Data, img.MimeType); err != nil {
			return status.Errorf(codes.InvalidArgument, "image %d: %v", i, err)
		}
	}
	for i, aud := range audios {
		if aud == nil {
			return status.Errorf(codes.InvalidArgument, "audio %d is empty", i)
		}
		if err := validateAudio(aud.Data, aud.MimeType); err != nil {
			return status.Errorf(codes.InvalidArgument, "audio %d: %v", i, err)
		}
	}
	return nil
}

// saveAttachments stops at the first failure and reports which attachment failed on which saved note.
func (s *NotesService) saveAttachments(ctx context.Context, note *db.Note, images []*pb.ImageUpload, audios []*pb.AudioUpload) error {
	l := logging.FromContext(ctx)
	noteID := note.ID
	for i, img := range images {
		noteImage, err := s.processAndUploadImage(ctx, noteID, img.Data, img.MimeType)
		if err != nil {
			l.Errorw("failed to upload image", "note_id", noteID, "image_index", i, zap.Error(err))
			return attachmentError(noteID, "image", i)
		}
		if err := s.db.AddImageToNote(ctx, noteID, noteImage); err != nil {
			l.Errorw("failed to save image to database", "note_id", noteID, "image_id", noteImage.ID, zap.Error(err))
			s.deleteOrphan(ctx, noteImage.GCSObjectName)
			return attachmentError(noteID, "image", i)
		}
		note.Images = append(note.Images, *noteImage)
	}
	for i, aud := range audios {
		noteAudio, err := s.processAndUploadAudio(ctx, noteID, aud.Data, aud.MimeType)
		if err != nil {
			l.Errorw("failed to upload audio", "note_id", noteID, "audio_index", i, zap.Error(err))
			return attachmentError(noteID, "audio", i)
		}
		if err := s.db.AddAudioToNote(ctx, noteID, noteAudio); err != nil {
			l.Errorw("failed to save audio to database", "note_id", noteID, "audio_id", noteAudio.ID, zap.Error(err))
			s.deleteOrphan(ctx, noteAudio.GCSObjectName)
			return attachmentError(noteID, "audio", i)
		}
		note.Audios = append(note.Audios, *noteAudio)
	}
	return nil
}

func (s *NotesService) deleteOrphan(ctx context.Context, object string) {
	if err := s.storage.DeleteImage(ctx, object); err != nil {
		logging.FromContext(ctx).Errorw("failed to clean up orphaned attachment", "object_name", object, zap.Error(err))
	}
}

func attachmentError(noteID, kind string, index int) error {
	st := status.New(codes.Internal, fmt.Sprintf("note %s was saved, but %s %d failed to upload; earlier attachments were kept", noteID, kind, index))
	detailed, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: "ATTACHMENT_UPLOAD_FAILED",
		Domain: "etu",
		Metadata: map[string]string{
			"note_id":          noteID,
			"attachment_type":  kind,
			"attachment_index": strconv.Itoa(index),
		},
	})
	if err != nil {
		return st.Err()
	}
	return detailed.Err()
}
