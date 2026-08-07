package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalBackendRoundTrip(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	b := NewLocal(root)

	src := filepath.Join(t.TempDir(), "node.log")
	if err := os.WriteFile(src, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := b.Upload(ctx, src, "job/run-1/node-0/stdout.log"); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := b.Write(ctx, "job/run-1/summary.json", []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, err := b.Read(ctx, "job/run-1/node-0/stdout.log")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "line one\nline two\n" {
		t.Fatalf("unexpected log content: %q", data)
	}

	sum, err := b.Read(ctx, "job/run-1/summary.json")
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !strings.Contains(string(sum), "true") {
		t.Fatalf("unexpected summary content: %s", sum)
	}

	if _, err := os.Stat(filepath.Join(root, "job/run-1/node-0/stdout.log")); err != nil {
		t.Fatalf("file not under root: %v", err)
	}
}

func TestLocalBackendRejectsEscape(t *testing.T) {
	ctx := context.Background()
	b := NewLocal(t.TempDir())

	for _, dest := range []string{"../../etc/passwd", "/etc/passwd", "..", "a/../../b"} {
		if err := b.Write(ctx, dest, []byte("x")); err == nil {
			t.Errorf("expected error for dest %q, got nil", dest)
		}
	}
}
