// Package terraform wraps the terraform-exec library: plan/apply/destroy with
// a per-run state key under the remote S3 backend.
package terraform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-exec/tfexec"
)

// BackendEnv names the env vars configuring the remote S3 state backend.
const (
	EnvStateBucket = "OPENBENCH_STATE_BUCKET"
	EnvStateRegion = "OPENBENCH_STATE_REGION"
	EnvStateLock   = "OPENBENCH_STATE_LOCK_TABLE"
)

// Runner drives the root Terraform config in dir (the repo's terraform/ dir).
type Runner struct {
	dir string
	tf  *tfexec.Terraform
}

func New(dir string) (*Runner, error) {
	tfPath, err := exec.LookPath("terraform")
	if err != nil {
		return nil, fmt.Errorf("terraform binary not found: %w (install HashiCorp Terraform)", err)
	}
	tf, err := tfexec.NewTerraform(dir, tfPath)
	if err != nil {
		return nil, fmt.Errorf("initializing terraform-exec: %w", err)
	}
	return &Runner{dir: dir, tf: tf}, nil
}

// stateBackendConfig returns the -backend-config flags derived from env.
func stateBackendConfig(stateKey string) ([]tfexec.InitOption, error) {
	bucket := os.Getenv(EnvStateBucket)
	region := os.Getenv(EnvStateRegion)
	lock := os.Getenv(EnvStateLock)
	if bucket == "" || region == "" || lock == "" {
		return nil, fmt.Errorf("%s, %s and %s must be set to use remote state",
			EnvStateBucket, EnvStateRegion, EnvStateLock)
	}
	return []tfexec.InitOption{
		tfexec.BackendConfig("bucket=" + bucket),
		tfexec.BackendConfig("region=" + region),
		tfexec.BackendConfig("dynamodb_table=" + lock),
		tfexec.BackendConfig("key=state/" + stateKey + "/terraform.tfstate"),
	}, nil
}

// Apply provisions infrastructure for a run. Each run gets its own state key
// (per-run isolation, not just per-run tfvars).
func (r *Runner) Apply(ctx context.Context, stateKey string, vars map[string]any) error {
	vf, err := r.writeVars(vars)
	if err != nil {
		return err
	}
	defer os.Remove(vf)

	initOpts, err := stateBackendConfig(stateKey)
	if err != nil {
		return err
	}
	// Each run uses its own state key, so init must reconfigure rather than
	// prompt for state migration.
	initOpts = append(initOpts, tfexec.Reconfigure(true))
	if err := r.tf.Init(ctx, initOpts...); err != nil {
		return fmt.Errorf("terraform init: %w", err)
	}
	// Apply passes -auto-approve itself in terraform-exec.
	if err := r.tf.Apply(ctx, tfexec.VarFile(vf)); err != nil {
		return fmt.Errorf("terraform apply: %w", err)
	}
	return nil
}

// Destroy tears down infrastructure. Unconditional by contract — the caller
// invokes it even after apply or test failures.
func (r *Runner) Destroy(ctx context.Context, stateKey string, vars map[string]any) error {
	vf, err := r.writeVars(vars)
	if err != nil {
		return err
	}
	defer os.Remove(vf)

	initOpts, err := stateBackendConfig(stateKey)
	if err != nil {
		return err
	}
	initOpts = append(initOpts, tfexec.Reconfigure(true))
	if err := r.tf.Init(ctx, initOpts...); err != nil {
		return fmt.Errorf("terraform init: %w", err)
	}
	if err := r.tf.Destroy(ctx, tfexec.VarFile(vf)); err != nil {
		return fmt.Errorf("terraform destroy: %w", err)
	}
	return nil
}

// Output reads a terraform output value (e.g. node_public_ips).
func (r *Runner) Output(ctx context.Context, name string) (string, error) {
	out, err := r.tf.Output(ctx)
	if err != nil {
		return "", fmt.Errorf("reading outputs: %w", err)
	}
	meta, ok := out[name]
	if !ok {
		return "", fmt.Errorf("terraform output %q not found", name)
	}
	return string(meta.Value), nil
}

func (r *Runner) writeVars(vars map[string]any) (string, error) {
	f, err := os.CreateTemp("", "openbench-*.tfvars")
	if err != nil {
		return "", fmt.Errorf("creating tfvars file: %w", err)
	}
	defer f.Close()

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Terraform HCL variables are flat scalar/collection values; render a
	// deterministic tfvars file.
	for _, k := range keys {
		if _, err := fmt.Fprintf(f, "%s = %s\n", k, hclValue(vars[k])); err != nil {
			return "", err
		}
	}
	return f.Name(), nil
}

func hclValue(v any) string {
	switch t := v.(type) {
	case string:
		return fmt.Sprintf("%q", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", t)
	case []string:
		parts := make([]string, 0, len(t))
		for _, s := range t {
			parts = append(parts, fmt.Sprintf("%q", s))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]string:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%q = %q", k, t[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Dir is the terraform root used by callers for display/debugging.
func (r *Runner) Dir() string { return filepath.Clean(r.dir) }
