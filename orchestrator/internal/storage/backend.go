// Package storage abstracts log delivery behind a single interface so the
// backend (local disk or S3) is a config choice, not an orchestrator concern.
package storage

import (
	"context"
	"fmt"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
)

// Backend stores and retrieves files. dest is a relative path like
// gpu-driver/{run_id}/node-0/stdout.log; the backend resolves it against its
// own root (local dir or S3 bucket).
type Backend interface {
	// Upload copies a local file into the backend at dest.
	Upload(ctx context.Context, localPath, dest string) error
	// Write stores raw bytes at dest.
	Write(ctx context.Context, dest string, data []byte) error
	// Read fetches raw bytes from dest.
	Read(ctx context.Context, dest string) ([]byte, error)
}

// New builds the backend selected by the job's storage config.
func New(ctx context.Context, cfg config.Storage) (Backend, error) {
	switch cfg.Type {
	case "local":
		return NewLocal(cfg.Dir), nil
	case "s3":
		return NewS3(ctx, cfg.Bucket)
	default:
		return nil, fmt.Errorf("unsupported storage type %q", cfg.Type)
	}
}
