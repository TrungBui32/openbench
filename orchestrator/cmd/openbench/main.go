package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
	"github.com/TrungBui32/openbench/orchestrator/internal/orchestrator"
	"github.com/TrungBui32/openbench/orchestrator/internal/runner"
	"github.com/TrungBui32/openbench/orchestrator/internal/runstore"
	"github.com/TrungBui32/openbench/orchestrator/internal/storage"
	"github.com/TrungBui32/openbench/orchestrator/internal/terraform"
	"github.com/TrungBui32/openbench/orchestrator/internal/ttlwatcher"
	"github.com/TrungBui32/openbench/orchestrator/pkg/runresult"
)

var version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "openbench",
		Short:         "OpenBench test orchestration platform",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(
		validateCmd(),
		dryRunCmd(),
		runCmd(),
		statusCmd(),
		logsCmd(),
		destroyCmd(),
		ttlWatchCmd(),
	)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <job.yaml>",
		Short: "Validate a job config against the JSON Schema and semantic rules",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			job, raw, err := config.Load(args[0])
			if err != nil {
				return err
			}
			if err := config.Validate(job, raw); err != nil {
				return err
			}
			fmt.Printf("OK: job %q valid (nodes=%d, storage=%s, trigger=%s)\n",
				job.Name, job.Nodes, job.Storage.Type, job.Trigger.Type)
			return nil
		},
	}
}

func dryRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dry-run <job.yaml>",
		Short: "Print the full execution plan without acting",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			job, raw, err := config.Load(args[0])
			if err != nil {
				return err
			}
			if err := config.Validate(job, raw); err != nil {
				return err
			}
			return renderDryRun(cmd, job)
		},
	}
}

func runCmd() *cobra.Command {
	var mode string
	var quiet bool
	c := &cobra.Command{
		Use:   "run <job.yaml>",
		Short: "Execute the full run lifecycle (provision -> test -> collect -> report)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			job, raw, err := config.Load(args[0])
			if err != nil {
				return err
			}
			if err := config.Validate(job, raw); err != nil {
				return err
			}
			if job.Infra.Provider == "aws" && mode != "k8s" {
				return fmt.Errorf("infra.provider %q requires --mode k8s", job.Infra.Provider)
			}

			backend, err := storage.New(ctx, job.Storage)
			if err != nil {
				return err
			}

			store, err := runstore.New()
			if err != nil {
				return err
			}

			provisioner, err := buildProvisioner(ctx, job)
			if err != nil {
				return err
			}

			builder := buildRunner(mode)
			orch := orchestrator.New(job, mode, builder, backend, store, provisioner)
			sum, err := orch.Run(ctx)
			if err != nil {
				return err
			}

			if !quiet {
				printSummary(cmd, sum)
			}
			if sum.Status == "failed" {
				return fmt.Errorf("run %s failed: %d/%d nodes failed", sum.RunID, sum.FailCount, len(sum.Nodes))
			}
			return nil
		},
	}
	c.Flags().StringVar(&mode, "mode", "docker", "execution backend: docker or k8s")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress the run summary on stdout")
	return c
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <run_id>",
		Short: "Show the current phase and outcome of a run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := runstore.New()
			if err != nil {
				return err
			}
			rec, err := store.Get(args[0])
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "run_id:      %s\n", rec.RunID)
			fmt.Fprintf(w, "job_name:    %s\n", rec.JobName)
			fmt.Fprintf(w, "mode:        %s\n", rec.Mode)
			fmt.Fprintf(w, "phase:       %s\n", rec.Phase)
			if rec.Error != "" {
				fmt.Fprintf(w, "error:       %s\n", rec.Error)
			}
			if rec.CleanupError != "" {
				fmt.Fprintf(w, "cleanup_error: %s\n", rec.CleanupError)
			}
			if rec.Summary != nil {
				fmt.Fprintf(w, "status:      %s\n", rec.Summary.Status)
				fmt.Fprintf(w, "pass/fail:   %d/%d\n", rec.Summary.PassCount, rec.Summary.FailCount)
			}
			return nil
		},
	}
}

func logsCmd() *cobra.Command {
	var node int
	c := &cobra.Command{
		Use:   "logs <run_id>",
		Short: "Print collected logs for a run (or one node)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := runstore.New()
			if err != nil {
				return err
			}
			rec, err := store.Get(args[0])
			if err != nil {
				return err
			}
			backend, err := storage.New(ctx, rec.Storage)
			if err != nil {
				return err
			}
			prefix := strings.TrimSuffix(strings.ReplaceAll(rec.Storage.PathPrefix, "{run_id}", rec.RunID), "/")

			nodes := []string{}
			if node >= 0 {
				nodes = append(nodes, fmt.Sprintf("node-%d", node))
			} else if rec.Summary != nil {
				for _, n := range rec.Summary.Nodes {
					nodes = append(nodes, fmt.Sprintf("node-%d", n.NodeID))
				}
			}
			if len(nodes) == 0 {
				return fmt.Errorf("no nodes recorded for run %s", rec.RunID)
			}

			w := cmd.OutOrStdout()
			for _, n := range nodes {
				fmt.Fprintf(w, "=== %s ===\n", n)
				data, err := backend.Read(ctx, path.Join(prefix, n, "stdout.log"))
				if err != nil {
					fmt.Fprintf(w, "(no logs collected for %s)\n", n)
					continue
				}
				fmt.Fprintln(w, string(data))
			}
			return nil
		},
	}
	c.Flags().IntVarP(&node, "node", "n", -1, "only print logs for this node index (-1 = all)")
	return c
}

func destroyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "destroy <run_id>",
		Short: "Force teardown of a run (manual, bypasses the TTL watcher)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			store, err := runstore.New()
			if err != nil {
				return err
			}
			rec, err := store.Get(args[0])
			if err != nil {
				return err
			}

			// Tear down any cloud infrastructure for the run before removing the
			// local metadata. Safe to call even if the original run process died;
			// terraform destroy is idempotent on the run's state key.
			if rec.Infra.Provider == "aws" {
				job := &config.Job{Nodes: rec.Nodes, Infra: rec.Infra}
				provisioner, err := buildProvisioner(ctx, job)
				if err != nil {
					return err
				}
				if closer, ok := provisioner.(interface{ Close() error }); ok {
					defer closer.Close()
				}
				if err := provisioner.Teardown(ctx, rec.RunID, job); err != nil {
					rec.CleanupError = err.Error()
					rec.Phase = "failed"
					_ = store.Save(rec)
					return fmt.Errorf("tearing down cloud infrastructure for run %s: %w", rec.RunID, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "run %s: cloud infrastructure destroyed\n", rec.RunID)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "run %s (mode=%s): no cloud infrastructure to destroy\n", rec.RunID, rec.Mode)
			}
			return store.Delete(rec.RunID)
		},
	}
}

func ttlWatchCmd() *cobra.Command {
	var interval time.Duration
	var region string
	var once bool
	c := &cobra.Command{
		Use:   "ttl-watch",
		Short: "Terminate AWS instances past their openbench TTL (crash-safety net)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			w, err := ttlwatcher.New(ctx, region)
			if err != nil {
				return err
			}
			if once {
				n, err := w.RunOnce(ctx)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "terminated %d expired instance(s)\n", n)
				return nil
			}
			return w.Watch(ctx, interval)
		},
	}
	c.Flags().DurationVar(&interval, "interval", 5*time.Minute, "how often to scan")
	c.Flags().StringVar(&region, "region", defaultRegion(), "AWS region")
	c.Flags().BoolVar(&once, "once", false, "scan once and exit")
	return c
}

// defaultRegion prefers the configured remote-state region so ttl-watch scans
// whichever region a run was created in, and falls back to us-east-1.
func defaultRegion() string {
	if r := os.Getenv(terraform.EnvStateRegion); r != "" {
		return r
	}
	return "us-east-1"
}

// buildRunner returns a RunnerBuilder for the requested execution backend. The
// builder is invoked by the orchestrator at run time, once the provisioned
// cluster kubeconfig is known (aws + k8s) or empty (local/default).
func buildRunner(mode string) orchestrator.RunnerBuilder {
	return func(_ context.Context, kubeconfig string) (runner.Runner, error) {
		switch mode {
		case "docker":
			return runner.NewDockerRunner()
		case "k8s":
			if kubeconfig != "" {
				return runner.NewK8sRunnerWithConfig(kubeconfig)
			}
			return runner.NewK8sRunner()
		default:
			return nil, fmt.Errorf("unknown mode %q (use docker or k8s)", mode)
		}
	}
}

// buildProvisioner returns the infra Provisioner for the job, or nil for
// local-only runs.
func buildProvisioner(ctx context.Context, job *config.Job) (terraform.Provisioner, error) {
	if job.Infra.Provider != "aws" {
		return nil, nil
	}
	dir := os.Getenv(terraform.EnvTerraformDir)
	if dir == "" {
		dir = "./terraform"
	}
	p, err := terraform.NewAWS(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("initializing infra provisioner: %w", err)
	}
	return p, nil
}

func renderDryRun(cmd *cobra.Command, job *config.Job) error {
	runID := runresult.NewRunID(job.Name)
	w := cmd.OutOrStdout()

	fmt.Fprintln(w, "--- OpenBench execution plan (dry-run, no actions taken) ---")
	fmt.Fprintf(w, "Job:              %s\n", job.Name)
	fmt.Fprintf(w, "Run ID:           %s\n", runID)
	fmt.Fprintf(w, "Image:            %s\n", job.Image)
	fmt.Fprintf(w, "Command:          %s\n", job.Command)
	fmt.Fprintf(w, "Nodes:            %d\n", job.Nodes)
	fmt.Fprintf(w, "On failure:       %s\n", job.OnFailure)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Infra:")
	fmt.Fprintf(w, "  provider:       %s\n", job.Infra.Provider)
	fmt.Fprintf(w, "  instance_type:  %s\n", job.Infra.InstanceType)
	fmt.Fprintf(w, "  region:         %s\n", job.Infra.Region)
	fmt.Fprintf(w, "  ttl_minutes:    %d\n", job.Infra.TTLMinutes)
	fmt.Fprintf(w, "  timeout_minutes:%d\n", job.TimeoutMinutes)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Storage:")
	fmt.Fprintf(w, "  type:           %s\n", job.Storage.Type)
	fmt.Fprintf(w, "  path_prefix:    %s\n", job.Storage.PathPrefix)
	if job.Storage.Type == "s3" {
		fmt.Fprintf(w, "  bucket:         %s\n", job.Storage.Bucket)
	} else {
		fmt.Fprintf(w, "  dir:            %s\n", job.Storage.Dir)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Kubernetes Job manifest preview:")
	manifest, err := runner.RenderJobManifest(job, runID)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, manifest)
	return nil
}

func printSummary(cmd *cobra.Command, sum *runresult.Summary) {
	raw, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), "--- Run summary ---")
	fmt.Fprintln(cmd.OutOrStdout(), string(raw))
}
