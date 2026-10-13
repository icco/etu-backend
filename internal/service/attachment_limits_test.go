package service

import (
	"context"
	"testing"

	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func images(n int) []*pb.ImageUpload {
	out := make([]*pb.ImageUpload, n)
	for i := range out {
		out[i] = &pb.ImageUpload{Data: []byte("x"), MimeType: "image/png"}
	}
	return out
}

func audios(n int) []*pb.AudioUpload {
	out := make([]*pb.AudioUpload, n)
	for i := range out {
		out[i] = &pb.AudioUpload{Data: []byte("x"), MimeType: "audio/mpeg"}
	}
	return out
}

func TestAttachmentCountLimits(t *testing.T) {
	svc := NewNotesService(nil, nil, nil, "")
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
		want codes.Code
	}{
		{
			name: "create too many images",
			call: func() error {
				_, err := svc.CreateNote(ctx, &pb.CreateNoteRequest{UserId: testUserID1, Content: "c", Images: images(MaxImagesPerRequest + 1)})
				return err
			},
			want: codes.InvalidArgument,
		},
		{
			name: "create too many audios",
			call: func() error {
				_, err := svc.CreateNote(ctx, &pb.CreateNoteRequest{UserId: testUserID1, Content: "c", Audios: audios(MaxAudiosPerRequest + 1)})
				return err
			},
			want: codes.InvalidArgument,
		},
		{
			name: "update too many images",
			call: func() error {
				_, err := svc.UpdateNote(ctx, &pb.UpdateNoteRequest{UserId: testUserID1, Id: "n1", AddImages: images(MaxImagesPerRequest + 1)})
				return err
			},
			want: codes.InvalidArgument,
		},
		{
			name: "update too many audios",
			call: func() error {
				_, err := svc.UpdateNote(ctx, &pb.UpdateNoteRequest{UserId: testUserID1, Id: "n1", AddAudios: audios(MaxAudiosPerRequest + 1)})
				return err
			},
			want: codes.InvalidArgument,
		},
		{
			name: "create at limit passes count check",
			call: func() error {
				_, err := svc.CreateNote(ctx, &pb.CreateNoteRequest{UserId: testUserID1, Images: images(MaxImagesPerRequest), Audios: audios(MaxAudiosPerRequest)})
				return err
			},
			want: codes.FailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Code(tt.call()); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
