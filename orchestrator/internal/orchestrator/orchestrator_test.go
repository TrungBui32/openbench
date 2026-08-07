package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
	"github.com/TrungBui32/openbench/orchestrator/internal/runner"
	"github.com/TrungBui32/openbench/orchestrator/internal/runstore"
	"github.com/TrungBui32/openbench/orchestrator/internal/storage"
	"github.com/TrungBui32/openbench/orchestrator/pkg/runresult"
)

type fakeRunner struct {
	results []runner.Result
	err     error
}

func (f *fakeRunner) Run(_ context.Context, _ *config.Job, _ string) ([]runner.Result, error) {
	return f.results, f.err
}

func TestRunProducesSummaryAndUploadsLogs(t *testing.T) {
	ctx := context.Background()
	logRoot := t.TempDir()
	t.Setenv("OPENBENCH_HOME", t.TempDir())

	job := &config.Job{
		Name:      "hello",
		Image:     "img:1",
		Command:   "echo hi",
		Nodes:     2,
		OnFailure: "collect-all",
		Infra:     config.Infra{Provider: "aws", InstanceType: "t3.small", Region: "us-east-1", TTLMinutes: 15},
		Storage:   config.Storage{Type: "local", Dir: logRoot, PathPrefix: "hello/{run_id}/"},
		Trigger:   config.Trigger{Type: "manual"},
	}

	tmp := t.TempDir()
	f1 := filepath.Join(tmp, "n0.log")
	f2 := filepath.Join(tmp, "n1.log")
	if err := os.WriteFile(f1, []byte("node-zero output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("node-one output\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results := []runner.Result{
		{NodeID: 0, ExitCode: 0, LogFile: f1},
		{NodeID: 1, ExitCode: 1, LogFile: f2},
	}

	store, err := runstore.New()
	if err != nil {
		t.Fatal(err)
	}
	backend := storage.NewLocal(logRoot)
	builder := func(_ context.Context, _ string) (runner.Runner, error) {
		return &fakeRunner{results: results}, nil
	}
	orch := New(job, "docker", builder, backend, store, nil)

	sum, err := orch.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if sum.Status != "failed" || sum.PassCount != 1 || sum.FailCount != 1 {
		t.Fatalf("unexpected summary: status=%s pass=%d fail=%d", sum.Status, sum.PassCount, sum.FailCount)
	}
	if len(sum.Nodes) != 2 || sum.Nodes[1].Status != runresult.StatusFail {
		t.Fatalf("unexpected nodes: %+v", sum.Nodes)
	}

	prefix := filepath.Join(logRoot, "hello", sum.RunID)
	log0, err := os.ReadFile(filepath.Join(prefix, "node-0", "stdout.log"))
	if err != nil || string(log0) != "node-zero output\n" {
		t.Fatalf("node-0 log missing/wrong: %v %q", err, log0)
	}

	raw, err := os.ReadFile(filepath.Join(prefix, "summary.json"))
	if err != nil {
		t.Fatalf("summary.json missing: %v", err)
	}
	var decoded runresult.Summary
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("summary.json invalid: %v", err)
	}
	if decoded.RunID != sum.RunID || decoded.JobName != "hello" {
		t.Fatalf("summary mismatch: %+v", decoded)
	}

	rec, err := store.Get(sum.RunID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if rec.Phase != "failed" || rec.Mode != "docker" {
		t.Fatalf("unexpected record: %+v", rec)
	}

	// Temp log files should have been cleaned up after upload.
	if _, err := os.Stat(f1); !os.IsNotExist(err) {
		t.Fatalf("temp log file %s should have been removed", f1)
	}
}
