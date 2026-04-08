package media

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	mediav1 "github.com/ananddub/ndiscord_backend/gen/media/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
)

// Handler implements the MediaServiceServer gRPC interface.
type Handler struct {
	mediav1.UnimplementedMediaServiceServer
	service *Service
}

// NewHandler creates a new media Handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RequestUpload creates a pending upload and returns a presigned PUT URL.
func (h *Handler) RequestUpload(ctx context.Context, req *mediav1.RequestUploadRequest) (*mediav1.RequestUploadResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user id")
	}

	if req.GetFilename() == "" {
		return nil, status.Error(codes.InvalidArgument, "filename is required")
	}

	result, err := h.service.RequestUpload(ctx, userID, req.GetFilename(), req.GetContentType(), req.GetSize())
	if err != nil {
		if errors.Is(err, ErrFileTooLarge) || errors.Is(err, ErrInvalidContentType) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "failed to request upload: %v", err)
	}

	return &mediav1.RequestUploadResponse{
		UploadId:  result.UploadID,
		UploadUrl: result.UploadURL,
	}, nil
}

// ConfirmUpload marks a file upload as confirmed.
func (h *Handler) ConfirmUpload(ctx context.Context, req *mediav1.ConfirmUploadRequest) (*mediav1.ConfirmUploadResponse, error) {
	if req.GetUploadId() == "" {
		return nil, status.Error(codes.InvalidArgument, "upload_id is required")
	}

	result, err := h.service.ConfirmUpload(ctx, req.GetUploadId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to confirm upload: %v", err)
	}

	return &mediav1.ConfirmUploadResponse{
		FileId: result.FileID,
		Url:    result.URL,
	}, nil
}

// GetDownloadURL generates a presigned GET URL for downloading a file.
func (h *Handler) GetDownloadURL(ctx context.Context, req *mediav1.GetDownloadURLRequest) (*mediav1.GetDownloadURLResponse, error) {
	if req.GetFileId() == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}

	result, err := h.service.GetDownloadURL(ctx, req.GetFileId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get download URL: %v", err)
	}

	return &mediav1.GetDownloadURLResponse{
		Url:       result.URL,
		ExpiresAt: timestamppb.New(result.ExpiresAt),
	}, nil
}

// DeleteFile removes a file from storage and the database.
func (h *Handler) DeleteFile(ctx context.Context, req *mediav1.DeleteFileRequest) (*mediav1.DeleteFileResponse, error) {
	if req.GetFileId() == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}

	if err := h.service.DeleteFile(ctx, req.GetFileId()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete file: %v", err)
	}

	return &mediav1.DeleteFileResponse{}, nil
}
