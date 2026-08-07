package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalBackend stores files under a root directory (storage.type: local).
type LocalBackend struct {
	root string
}

func NewLocal(root string) *LocalBackend {
	return &LocalBackend{root: filepath.Clean(root)}
}

func (b *LocalBackend) Upload(ctx context.Context, localPath, dest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	src, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", localPath, err)
	}
	defer src.Close()

	target, err := b.resolve(dest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
	}
	dst, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("creating %s: %w", target, err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copying %s: %w", target, err)
	}
	return nil
}

func (b *LocalBackend) Write(ctx context.Context, dest string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := b.resolve(dest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", target, err)
	}
	return nil
}

func (b *LocalBackend) Read(ctx context.Context, dest string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := b.resolve(dest)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", target, err)
	}
	return data, nil
}

// resolve maps a relative dest into the root, refusing any path that escapes
// the root directory.
func (b *LocalBackend) resolve(dest string) (string, error) {
	if dest == "" {
		return "", fmt.Errorf("empty destination")
	}
	clean := filepath.Clean(dest)
	if clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("destination %q escapes storage root", dest)
	}
	return filepath.Join(b.root, clean), nil
}
