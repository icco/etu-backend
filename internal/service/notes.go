package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/icco/etu-backend/internal/ai"
	"github.com/icco/etu-backend/internal/db"
	"github.com/icco/etu-backend/internal/models"
	"github.com/icco/etu-backend/internal/storage"
	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	MaxNotesLimit     = 100
	DefaultNotesLimit = 50
	MaxImageSize      = 10 * 1024 * 1024 // 10MB max image size
	MaxAudioSize      = 25 * 1024 * 1024 // 25MB max audio size
)

// NotesService implements the NotesService gRPC service
type NotesService struct {
	pb.UnimplementedNotesServiceServer
	db          *db.DB
	storage     *storage.Client
	aiClient    *ai.Client
	imgixDomain string
	log         *slog.Logger
}

// NewNotesService creates a new NotesService
func NewNotesService(database *db.DB, storageClient *storage.Client, aiClient *ai.Client, imgixDomain string) *NotesService {
	return &NotesService{
		db:          database,
		storage:     storageClient,
		aiClient:    aiClient,
		imgixDomain: imgixDomain,
		log:         slog.Default(),
	}
}

// ListNotes retrieves notes for a user with optional filtering
func (s *NotesService) ListNotes(ctx context.Context, req *pb.ListNotesRequest) (*pb.ListNotesResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Verify authorization
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = DefaultNotesLimit
	}
	if limit > MaxNotesLimit {
		limit = MaxNotesLimit
	}

	offset := int(req.Offset)
	if offset < 0 {
		offset = 0
	}

	notes, total, err := s.db.ListNotes(ctx, req.UserId, req.Search, req.Tags, req.StartDate, req.EndDate, limit, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list notes: %v", err)
	}

	pbNotes := make([]*pb.Note, len(notes))
	for i, n := range notes {
		pbNotes[i] = s.noteToProto(&n)
	}

	return &pb.ListNotesResponse{
		Notes:  pbNotes,
		Total:  int32(total),
		Limit:  int32(limit),
		Offset: int32(offset),
	}, nil
}

// CreateNote creates a new note
func (s *NotesService) CreateNote(ctx context.Context, req *pb.CreateNoteRequest) (*pb.CreateNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Content == "" && len(req.Images) == 0 && len(req.Audios) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one of content, images, or audio files is required")
	}
	if req.Content == "" && (len(req.Images) > 0 || len(req.Audios) > 0) && s.storage == nil {
		return nil, status.Error(codes.FailedPrecondition, "storage is not configured")
	}

	// Verify authorization
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	note, err := s.db.CreateNote(ctx, req.UserId, req.Content, req.Tags)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create note: %v", err)
	}

	// Process images if any
	if len(req.Images) > 0 && s.storage != nil {
		for i, img := range req.Images {
			// Upload image to GCS
			noteImage, err := s.processAndUploadImage(ctx, note.ID, img.Data, img.MimeType)
			if err != nil {
				s.log.Error("failed to process image", "note_id", note.ID, "image_index", i, "error", err)
				continue // Continue with other images even if one fails
			}

			// Add image to database
			if err := s.db.AddImageToNote(ctx, note.ID, noteImage); err != nil {
				s.log.Error("failed to save image to database", "note_id", note.ID, "image_id", noteImage.ID, "error", err)
				// Try to clean up the uploaded image
				if s.storage != nil {
					if deleteErr := s.storage.DeleteImage(ctx, noteImage.GCSObjectName); deleteErr != nil {
						s.log.Error("failed to clean up image from GCS after DB error", "object_name", noteImage.GCSObjectName, "error", deleteErr)
					}
				}
				continue
			}

			note.Images = append(note.Images, *noteImage)
		}
	}

	// Process audio files if any
	if len(req.Audios) > 0 && s.storage != nil {
		for i, aud := range req.Audios {
			// Upload audio to GCS
			noteAudio, err := s.processAndUploadAudio(ctx, note.ID, aud.Data, aud.MimeType)
			if err != nil {
				s.log.Error("failed to process audio", "note_id", note.ID, "audio_index", i, "error", err)
				continue // Continue with other audios even if one fails
			}

			// Add audio to database
			if err := s.db.AddAudioToNote(ctx, note.ID, noteAudio); err != nil {
				s.log.Error("failed to save audio to database", "note_id", note.ID, "audio_id", noteAudio.ID, "error", err)
				// Try to clean up the uploaded audio
				if s.storage != nil {
					if deleteErr := s.storage.DeleteImage(ctx, noteAudio.GCSObjectName); deleteErr != nil {
						s.log.Error("failed to clean up audio from GCS after DB error", "object_name", noteAudio.GCSObjectName, "error", deleteErr)
					}
				}
				continue
			}

			note.Audios = append(note.Audios, *noteAudio)
		}
	}

	return &pb.CreateNoteResponse{
		Note: s.noteToProto(note),
	}, nil
}

// validateImage validates the image MIME type and size
func validateImage(imageData []byte, mimeType string) error {
	// Validate MIME type against allow-list
	if !ai.IsValidImageMimeType(mimeType) {
		return fmt.Errorf("unsupported image type: %s. Allowed types: image/jpeg, image/png, image/gif, image/webp, image/heic, image/heif", mimeType)
	}

	// Validate image size
	if len(imageData) > MaxImageSize {
		return fmt.Errorf("image size %d bytes exceeds maximum allowed size of %d bytes", len(imageData), MaxImageSize)
	}

	return nil
}

// processAndUploadImage uploads an image to GCS and extracts text using Gemini OCR
func (s *NotesService) processAndUploadImage(ctx context.Context, noteID string, imageData []byte, mimeType string) (*models.NoteImage, error) {
	if s.storage == nil {
		return nil, fmt.Errorf("storage client not configured")
	}

	// Validate image before uploading
	if err := validateImage(imageData, mimeType); err != nil {
		return nil, err
	}

	// Generate a unique object name
	imageID := models.GenerateCUID()
	objectName := fmt.Sprintf("notes/%s/%s", noteID, imageID)

	// Upload to GCS
	url, err := s.storage.UploadImage(ctx, objectName, imageData, mimeType)
	if err != nil {
		return nil, fmt.Errorf("failed to upload image: %w", err)
	}

	// Note: Text extraction is handled asynchronously by a background job for performance
	return &models.NoteImage{
		ID:            imageID,
		NoteID:        noteID,
		URL:           url,
		GCSObjectName: objectName,
		ExtractedText: "", // Will be filled by background job
		MimeType:      mimeType,
	}, nil
}

// validateAudio validates the audio MIME type and size
func validateAudio(audioData []byte, mimeType string) error {
	// Validate MIME type against allow-list
	if !ai.IsValidAudioMimeType(mimeType) {
		return fmt.Errorf("unsupported audio type: %s. Allowed types: audio/mpeg, audio/mp3, audio/wav, audio/wave, audio/ogg, audio/webm, audio/mp4, audio/m4a, audio/flac, audio/aac", mimeType)
	}

	// Validate audio size
	if len(audioData) > MaxAudioSize {
		return fmt.Errorf("audio size %d bytes exceeds maximum allowed size of %d bytes", len(audioData), MaxAudioSize)
	}

	return nil
}

// processAndUploadAudio uploads an audio file to GCS and transcribes it using Gemini
func (s *NotesService) processAndUploadAudio(ctx context.Context, noteID string, audioData []byte, mimeType string) (*models.NoteAudio, error) {
	if s.storage == nil {
		return nil, fmt.Errorf("storage client not configured")
	}

	// Validate audio before uploading
	if err := validateAudio(audioData, mimeType); err != nil {
		return nil, err
	}

	// Generate a unique object name
	audioID := models.GenerateCUID()
	objectName := fmt.Sprintf("notes/%s/%s", noteID, audioID)

	// Upload to GCS
	url, err := s.storage.UploadImage(ctx, objectName, audioData, mimeType)
	if err != nil {
		return nil, fmt.Errorf("failed to upload audio: %w", err)
	}

	// Note: Transcription is handled asynchronously by a background job for performance
	return &models.NoteAudio{
		ID:              audioID,
		NoteID:          noteID,
		URL:             url,
		GCSObjectName:   objectName,
		TranscribedText: "", // Will be filled by background job
		MimeType:        mimeType,
	}, nil
}

// GetNote retrieves a single note by ID
func (s *NotesService) GetNote(ctx context.Context, req *pb.GetNoteRequest) (*pb.GetNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	// Verify authorization
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	note, err := s.db.GetNote(ctx, req.UserId, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get note: %v", err)
	}
	if note == nil {
		return nil, status.Error(codes.NotFound, "note not found")
	}

	return &pb.GetNoteResponse{
		Note: s.noteToProto(note),
	}, nil
}

// UpdateNote updates an existing note
func (s *NotesService) UpdateNote(ctx context.Context, req *pb.UpdateNoteRequest) (*pb.UpdateNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	// Verify authorization
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	var content *string
	if req.Content != nil {
		content = req.Content
	}

	note, err := s.db.UpdateNote(ctx, req.UserId, req.Id, content, req.Tags, req.UpdateTags)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update note: %v", err)
	}
	if note == nil {
		return nil, status.Error(codes.NotFound, "note not found")
	}

	// Add new images if any
	if len(req.AddImages) > 0 && s.storage != nil {
		for i, img := range req.AddImages {
			noteImage, err := s.processAndUploadImage(ctx, note.ID, img.Data, img.MimeType)
			if err != nil {
				s.log.Error("failed to process image", "note_id", note.ID, "image_index", i, "error", err)
				continue
			}

			if err := s.db.AddImageToNote(ctx, note.ID, noteImage); err != nil {
				s.log.Error("failed to save image to database", "note_id", note.ID, "image_id", noteImage.ID, "error", err)
				if s.storage != nil {
					if deleteErr := s.storage.DeleteImage(ctx, noteImage.GCSObjectName); deleteErr != nil {
						s.log.Error("failed to clean up image from GCS after DB error", "object_name", noteImage.GCSObjectName, "error", deleteErr)
					}
				}
				continue
			}
		}
	}

	// Add new audio files if any
	if len(req.AddAudios) > 0 && s.storage != nil {
		for i, aud := range req.AddAudios {
			noteAudio, err := s.processAndUploadAudio(ctx, note.ID, aud.Data, aud.MimeType)
			if err != nil {
				s.log.Error("failed to process audio", "note_id", note.ID, "audio_index", i, "error", err)
				continue
			}

			if err := s.db.AddAudioToNote(ctx, note.ID, noteAudio); err != nil {
				s.log.Error("failed to save audio to database", "note_id", note.ID, "audio_id", noteAudio.ID, "error", err)
				if s.storage != nil {
					if deleteErr := s.storage.DeleteImage(ctx, noteAudio.GCSObjectName); deleteErr != nil {
						s.log.Error("failed to clean up audio from GCS after DB error", "object_name", noteAudio.GCSObjectName, "error", deleteErr)
					}
				}
				continue
			}
		}
	}

	// Reload note to get updated images and audios
	note, err = s.db.GetNote(ctx, req.UserId, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to reload note: %v", err)
	}

	return &pb.UpdateNoteResponse{
		Note: s.noteToProto(note),
	}, nil
}

// DeleteNote deletes a note by ID
func (s *NotesService) DeleteNote(ctx context.Context, req *pb.DeleteNoteRequest) (*pb.DeleteNoteResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	// Verify authorization
	if err := verifyUserAuthorization(ctx, req.UserId); err != nil {
		return nil, err
	}

	// Get images before deleting the note so we can clean them up from GCS
	images, err := s.db.GetImagesByNoteID(ctx, req.Id)
	if err != nil {
		s.log.Warn("failed to get images for note before deletion", "note_id", req.Id, "error", err)
	}

	// Get audio files before deleting the note so we can clean them up from GCS
	audios, err := s.db.GetAudiosByNoteID(ctx, req.Id)
	if err != nil {
		s.log.Warn("failed to get audios for note before deletion", "note_id", req.Id, "error", err)
	}

	deleted, err := s.db.DeleteNote(ctx, req.UserId, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete note: %v", err)
	}

	// Clean up images from GCS if the note was deleted
	if deleted && s.storage != nil {
		for _, img := range images {
			if err := s.storage.DeleteImage(ctx, img.GCSObjectName); err != nil {
				s.log.Error("failed to delete image from GCS", "object_name", img.GCSObjectName, "error", err)
			}
		}
	}

	// Clean up audio files from GCS if the note was deleted
	if deleted && s.storage != nil {
		for _, aud := range audios {
			if err := s.storage.DeleteImage(ctx, aud.GCSObjectName); err != nil {
				s.log.Error("failed to delete audio from GCS", "object_name", aud.GCSObjectName, "error", err)
			}
		}
	}

	return &pb.DeleteNoteResponse{
		Success: deleted,
	}, nil
}

// getImageURL returns the appropriate URL for an image.
// If imgix is configured, it returns an imgix URL using the GCS object name.
// Otherwise, it returns the original GCS signed URL.
func (s *NotesService) getImageURL(img *models.NoteImage) string {
	if s.imgixDomain != "" && img.GCSObjectName != "" {
		return fmt.Sprintf("https://%s/%s", s.imgixDomain, img.GCSObjectName)
	}
	return img.URL
}

// getAudioURL returns the appropriate URL for an audio file.
// If imgix is configured, it returns an imgix URL using the GCS object name.
// Otherwise, it returns the original GCS signed URL.
func (s *NotesService) getAudioURL(aud *models.NoteAudio) string {
	if s.imgixDomain != "" && aud.GCSObjectName != "" {
		return fmt.Sprintf("https://%s/%s", s.imgixDomain, aud.GCSObjectName)
	}
	return aud.URL
}

// noteToProto converts a db.Note to a protobuf Note
func (s *NotesService) noteToProto(n *db.Note) *pb.Note {
	// Convert []Tag to []string
	tagNames := make([]string, len(n.Tags))
	for i, t := range n.Tags {
		tagNames[i] = t.Name
	}

	// Convert []NoteImage to []*pb.NoteImage
	pbImages := make([]*pb.NoteImage, len(n.Images))
	for i, img := range n.Images {
		pbImages[i] = &pb.NoteImage{
			Id:            img.ID,
			Url:           s.getImageURL(&img),
			ExtractedText: img.ExtractedText,
			MimeType:      img.MimeType,
			CreatedAt:     timestamppb.New(img.CreatedAt),
		}
	}

	// Convert []NoteAudio to []*pb.NoteAudio
	pbAudios := make([]*pb.NoteAudio, len(n.Audios))
	for i, aud := range n.Audios {
		pbAudios[i] = &pb.NoteAudio{
			Id:              aud.ID,
			Url:             s.getAudioURL(&aud),
			TranscribedText: aud.TranscribedText,
			MimeType:        aud.MimeType,
			CreatedAt:       timestamppb.New(aud.CreatedAt),
		}
	}

	return &pb.Note{
		Id:        n.ID,
		Content:   n.Content,
		Tags:      tagNames,
		CreatedAt: timestamppb.New(n.CreatedAt),
		UpdatedAt: timestamppb.New(n.UpdatedAt),
		Images:    pbImages,
		Audios:    pbAudios,
	}
}
