package runstore

import (
	"testing"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
	"github.com/TrungBui32/openbench/orchestrator/pkg/runresult"
)

func TestStoreRoundTrip(t *testing.T) {
	t.Setenv("OPENBENCH_HOME", t.TempDir())
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}

	rec := &Record{
		RunID:   "x-20260803-0000",
		JobName: "x",
		Mode:    "docker",
		Phase:   "done",
		Storage: config.Storage{Type: "s3", Bucket: "b", PathPrefix: "x/{run_id}/"},
		Summary: &runresult.Summary{RunID: "x-20260803-0000", Status: "failed", PassCount: 1, FailCount: 2},
	}
	if err := s.Save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.Get("x-20260803-0000")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Storage.Type != "s3" || got.Storage.Bucket != "b" || got.Storage.PathPrefix != "x/{run_id}/" {
		t.Fatalf("storage roundtrip mismatch: %+v", got.Storage)
	}
	if got.Summary.PassCount != 1 || got.Summary.FailCount != 2 {
		t.Fatalf("summary roundtrip mismatch: %+v", got.Summary)
	}

	if err := s.Delete("x-20260803-0000"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get("x-20260803-0000"); err == nil {
		t.Fatal("expected not-found error after delete")
	}
}
