// Package runstore persists lightweight per-run metadata so status/logs/destroy
// can locate a finished run. Local to the orchestrator host (~/.openbench/runs);
// not a source of truth — the run summary in the storage backend is.
package runstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
	"github.com/TrungBui32/openbench/orchestrator/pkg/runresult"
)

// runIDPattern restricts run ids to a single, filesystem-safe path segment so
// a malicious or malformed id cannot escape the run store directory.
var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Record is the persisted metadata for one run.
type Record struct {
	RunID   string             `json:"run_id"`
	JobName string             `json:"job_name"`
	Mode    string             `json:"mode"`  // docker | k8s
	Phase   string             `json:"phase"` // provisioning | running | done | failed
	Nodes   int                `json:"nodes"` // parallel replicas (needed for teardown)
	Infra   config.Infra       `json:"infra,omitempty"`
	Storage config.Storage     `json:"storage"`
	Summary *runresult.Summary `json:"summary,omitempty"`
}

type Store struct {
	dir string
}

// New returns a Store rooted at $OPENBENCH_HOME/runs, or ~/.openbench/runs.
func New() (*Store, error) {
	base := os.Getenv("OPENBENCH_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolving home dir: %w", err)
		}
		base = filepath.Join(home, ".openbench")
	}
	dir := filepath.Join(base, "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating run store %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(runID string) (string, error) {
	if runID == "" || !runIDPattern.MatchString(runID) {
		return "", fmt.Errorf("invalid run id %q", runID)
	}
	return filepath.Join(s.dir, runID+".json"), nil
}

func (s *Store) Save(rec *Record) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding record: %w", err)
	}
	p, err := s.path(rec.RunID)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return fmt.Errorf("writing record: %w", err)
	}
	return nil
}

func (s *Store) Get(runID string) (*Record, error) {
	p, err := s.path(runID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("run %s not found", runID)
		}
		return nil, fmt.Errorf("reading record: %w", err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("decoding record: %w", err)
	}
	return &rec, nil
}

func (s *Store) Delete(runID string) error {
	p, err := s.path(runID)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing record: %w", err)
	}
	return nil
}
