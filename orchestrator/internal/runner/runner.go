// Package runner defines the execution abstraction behind which the local
// Docker path and the Kubernetes path live.
package runner

import (
	"context"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
)

// Result is one node's outcome within a run. LogFile is the absolute path of
// the captured combined stdout/stderr on the orchestrator host (empty if the
// node never produced logs).
type Result struct {
	NodeID   int
	ExitCode int
	LogFile  string
	Err      error
}

// Failed reports whether the node's test outcome is a failure.
func (r Result) Failed() bool {
	if r.Err != nil {
		return true
	}
	return r.ExitCode != 0
}

// Runner executes a job across N nodes. Implementations: DockerRunner and
// K8sRunner.
type Runner interface {
	Run(ctx context.Context, job *config.Job, runID string) ([]Result, error)
}
