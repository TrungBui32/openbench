// Package orchestrator ties the pieces together behind one config file:
// run_id generation, infrastructure provisioning, execution via a Runner,
// log upload via a StorageBackend, and emission of the structured run summary.
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
	"github.com/TrungBui32/openbench/orchestrator/internal/runner"
	"github.com/TrungBui32/openbench/orchestrator/internal/runstore"
	"github.com/TrungBui32/openbench/orchestrator/internal/storage"
	"github.com/TrungBui32/openbench/orchestrator/internal/terraform"
	"github.com/TrungBui32/openbench/orchestrator/pkg/runresult"
)

// RunnerBuilder constructs the execution backend. kubeconfig is empty for
// local execution; when set, the runner must target that cluster (the
// kubeconfig staged during provisioning).
type RunnerBuilder func(ctx context.Context, kubeconfig string) (runner.Runner, error)

// Orchestrator runs one job through its full lifecycle.
type Orchestrator struct {
	job     *config.Job
	mode    string
	builder RunnerBuilder
	backend storage.Backend
	store   *runstore.Store
	infra   terraform.Provisioner
}

// New wires the pieces. infra may be nil for local-only runs.
func New(job *config.Job, mode string, builder RunnerBuilder, backend storage.Backend, store *runstore.Store, infra terraform.Provisioner) *Orchestrator {
	return &Orchestrator{job: job, mode: mode, builder: builder, backend: backend, store: store, infra: infra}
}

// Run executes the full lifecycle for a configured job and returns the
// structured summary. The returned error is non-nil only for
// infrastructure-level failures; per-node test failures are reflected in the
// summary's status/counts.
//
// When the job declares AWS infra, Provision runs first and the teardown runs
// unconditionally afterwards (deferred, so even a test failure or provisioning
// error still attempts cleanup). The persisted run record walks
// provisioning -> running -> done (or failed).
func (o *Orchestrator) Run(ctx context.Context) (sum *runresult.Summary, runErr error) {
	runID := runresult.NewRunID(o.job.Name)
	started := time.Now().UTC()
	if closer, ok := o.infra.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	cleanupError := ""

	if err := o.saveRecord(runID, "provisioning", nil); err != nil {
		return nil, fmt.Errorf("saving run before provisioning: %w", err)
	}
	defer func() {
		phase := "done"
		if runErr != nil || (sum != nil && sum.Status == "failed") {
			phase = "failed"
		}
		if o.store != nil {
			rec := &runstore.Record{RunID: runID, JobName: o.job.Name, Mode: o.mode, Phase: phase, Nodes: o.job.Nodes, Infra: o.job.Infra, Storage: o.job.Storage, Summary: sum, CleanupError: cleanupError}
			if runErr != nil {
				rec.Error = runErr.Error()
			}
			runErr = errors.Join(runErr, o.store.Save(rec))
		}
	}()

	var provisioned *terraform.ProvisionOutput
	if o.infra != nil && o.job.Infra.Provider == "aws" {
		// Register teardown before provisioning so any partial create (apply
		// succeeded but a later step failed) still releases the instances.
		// Teardown is documented as safe to call even when provision failed.
		defer func() {
			if err := o.infra.Teardown(context.Background(), runID, o.job); err != nil {
				cleanupError = err.Error()
				runErr = errors.Join(runErr, fmt.Errorf("teardown for run %s failed; retry with openbench destroy %s: %w", runID, runID, err))
			}
		}()
		po, err := o.infra.Provision(ctx, runID, o.job)
		if err != nil {
			return nil, fmt.Errorf("provisioning: %w", err)
		}
		provisioned = po
	}

	kubeconfig := ""
	if provisioned != nil {
		kubeconfig = provisioned.KubeconfigPath
	}
	r, err := o.builder(ctx, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("building runner: %w", err)
	}
	if closer, ok := r.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	if err := o.saveRecord(runID, "running", nil); err != nil {
		return nil, err
	}

	var results []runner.Result
	// Clean up captured log files after the run, including on error paths.
	defer func() {
		for _, res := range results {
			if res.LogFile != "" {
				_ = os.Remove(res.LogFile)
			}
		}
	}()

	results, err = r.Run(ctx, o.job, runID)
	if err != nil {
		return nil, fmt.Errorf("run failed: %w", err)
	}

	prefix := strings.TrimSuffix(strings.ReplaceAll(o.job.Storage.PathPrefix, "{run_id}", runID), "/")

	pass, fail := 0, 0
	nodes := make([]runresult.NodeResult, len(results))
	for i, r := range results {
		status := runresult.StatusPass
		if r.Failed() {
			status = runresult.StatusFail
		}
		if status == runresult.StatusPass {
			pass++
		} else {
			fail++
		}

		nodeDir := path.Join(prefix, fmt.Sprintf("node-%d", r.NodeID)) + "/"
		if r.LogFile != "" {
			dest := path.Join(prefix, fmt.Sprintf("node-%d", r.NodeID), "stdout.log")
			if err := o.backend.Upload(ctx, r.LogFile, dest); err != nil {
				return nil, fmt.Errorf("uploading node %d logs: %w", r.NodeID, err)
			}
		}
		nodes[i] = runresult.NodeResult{
			NodeID:   r.NodeID,
			ExitCode: r.ExitCode,
			Status:   status,
			LogPath:  nodeDir,
		}
	}

	finished := time.Now().UTC()
	sum = &runresult.Summary{
		RunID:           runID,
		JobName:         o.job.Name,
		OnFailurePolicy: o.job.OnFailure,
		StartedAt:       started,
		FinishedAt:      finished,
		Nodes:           nodes,
		PassCount:       pass,
		FailCount:       fail,
		Infra: runresult.InfraInfo{
			Provider:     o.job.Infra.Provider,
			InstanceType: o.job.Infra.InstanceType,
			Region:       o.job.Infra.Region,
		},
	}
	sum.Status = "passed"
	if fail > 0 {
		sum.Status = "failed"
	}

	raw, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding summary: %w", err)
	}
	if err := o.backend.Write(ctx, path.Join(prefix, "summary.json"), raw); err != nil {
		return nil, fmt.Errorf("writing summary: %w", err)
	}

	if err := o.saveRecord(runID, "running", sum); err != nil {
		return sum, err
	}
	return sum, nil
}

func (o *Orchestrator) saveRecord(runID, phase string, sum *runresult.Summary) error {
	if o.store == nil {
		return nil
	}
	return o.store.Save(&runstore.Record{
		RunID:   runID,
		JobName: o.job.Name,
		Mode:    o.mode,
		Phase:   phase,
		Nodes:   o.job.Nodes,
		Infra:   o.job.Infra,
		Storage: o.job.Storage,
		Summary: sum,
	})
}
