package orchestrator

import (
	"context"
	"testing"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
	"github.com/TrungBui32/openbench/orchestrator/internal/runner"
	"github.com/TrungBui32/openbench/orchestrator/internal/runstore"
	"github.com/TrungBui32/openbench/orchestrator/internal/storage"
	"github.com/TrungBui32/openbench/orchestrator/internal/terraform"
)

type fakeProvisioner struct {
	provisioned bool
	tornDown    bool
	output      *terraform.ProvisionOutput
	provErr     error
	tearErr     error
}

func (f *fakeProvisioner) Provision(_ context.Context, _ string, _ *config.Job) (*terraform.ProvisionOutput, error) {
	f.provisioned = true
	return f.output, f.provErr
}

func (f *fakeProvisioner) Teardown(_ context.Context, _ string, _ *config.Job) error {
	f.tornDown = true
	return f.tearErr
}

func awsJob(logRoot string) *config.Job {
	return &config.Job{
		Name:      "awsjob",
		Image:     "img:1",
		Command:   "echo hi",
		Nodes:     1,
		OnFailure: "collect-all",
		Infra:     config.Infra{Provider: "aws", InstanceType: "t3.small", Region: "us-east-1", TTLMinutes: 15},
		Storage:   config.Storage{Type: "local", Dir: logRoot, PathPrefix: "awsjob/{run_id}/"},
		Trigger:   config.Trigger{Type: "manual"},
	}
}

func TestRunProvisionsBeforeRunAndTearsDown(t *testing.T) {
	ctx := context.Background()
	logRoot := t.TempDir()
	t.Setenv("OPENBENCH_HOME", t.TempDir())

	prov := &fakeProvisioner{output: &terraform.ProvisionOutput{KubeconfigPath: "/tmp/k3s.yaml"}}
	builder := func(_ context.Context, kubeconfig string) (runner.Runner, error) {
		if kubeconfig != "/tmp/k3s.yaml" {
			t.Fatalf("expected provisioned kubeconfig path, got %q", kubeconfig)
		}
		return &fakeRunner{results: []runner.Result{{NodeID: 0, ExitCode: 0}}}, nil
	}

	store, _ := runstore.New()
	orbit := New(awsJob(logRoot), "k8s", builder, storage.NewLocal(logRoot), store, prov)
	sum, err := orbit.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !prov.provisioned {
		t.Fatal("expected provision to run")
	}
	if !prov.tornDown {
		t.Fatal("expected teardown to run")
	}

	rec, err := store.Get(sum.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Phase != "done" {
		t.Fatalf("final phase = %q, want done", rec.Phase)
	}
	if rec.Infra.Provider != "aws" {
		t.Fatalf("record infra missing: %+v", rec.Infra)
	}
}

func TestRunProvisionsButSkipsRunOnProvisionError(t *testing.T) {
	ctx := context.Background()
	logRoot := t.TempDir()
	t.Setenv("OPENBENCH_HOME", t.TempDir())

	prov := &fakeProvisioner{provErr: context.Canceled}
	ran := false
	builder := func(_ context.Context, _ string) (runner.Runner, error) {
		ran = true
		return &fakeRunner{}, nil
	}

	store, _ := runstore.New()
	orbit := New(awsJob(logRoot), "k8s", builder, storage.NewLocal(logRoot), store, prov)
	if _, err := orbit.Run(ctx); err == nil {
		t.Fatal("expected provisioning error to propagate")
	}
	if ran {
		t.Fatal("runner should not run when provisioning fails")
	}
	if !prov.tornDown {
		t.Fatal("teardown should still be attempted after a failed provision (partial creates must be released)")
	}
}
