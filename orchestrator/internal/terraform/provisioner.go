package terraform

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
)

// EnvTerraformDir overrides the location of the root terraform config (the
// repo's terraform/ dir). Defaults to ./terraform relative to the CWD.
const EnvTerraformDir = "OPENBENCH_TERRAFORM_DIR"

// kubeconfigS3Key is the S3 object key, under the remote state bucket, where
// node 0 base64-encodes its rewritten kubeconfig after k3s bootstraps.
const kubeconfigS3Key = "kubeconfig/{run_id}/k3s.yaml.b64"

// serverURLS3Key is where node 0 publishes the cluster URL for joining agents.
const serverURLS3Key = "kubeconfig/{run_id}/server-url"

// ProvisionOutput carries everything a runner needs to reach the cluster the
// provisioning step stood up.
type ProvisionOutput struct {
	KubeconfigPath string
	K3sURL         string
	NodePublicIPs  []string
}

// Provisioner drives the full infrastructure lifecycle for a run. Provision is
// idempotent-per-run via the state key; Teardown is safe to call even if
// Provision failed partway through (it just destroys whatever exists).
type Provisioner interface {
	Provision(ctx context.Context, runID string, job *config.Job) (*ProvisionOutput, error)
	Teardown(ctx context.Context, runID string, job *config.Job) error
}

// AWS is the cloud provider backed by the repo's terraform config.
type AWS struct {
	runner *Runner
	s3     *s3.Client
}

// NewAWS builds a Provisioner rooted at the terraform config in dir.
func NewAWS(ctx context.Context, dir string) (*AWS, error) {
	r, err := New(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &AWS{runner: r, s3: s3.NewFromConfig(cfg)}, nil
}

// Provision applies the terraform config for runID, forces k3s on, then fetches
// the cluster kubeconfig it staged in S3 and returns a reference to it.
func (a *AWS) Provision(ctx context.Context, runID string, job *config.Job) (*ProvisionOutput, error) {
	vars := map[string]any{
		"nodes":         job.Nodes,
		"instance_type": job.Infra.InstanceType,
		"region":        job.Infra.Region,
		"run_id":        runID,
		"ttl_minutes":   job.Infra.TTLMinutes,
		"spot":          job.Infra.Spot,
		"enable_k3s":    true,
		"state_bucket":  os.Getenv(EnvStateBucket),
		"state_region":  os.Getenv(EnvStateRegion),
		"tags":          varsTag(job),
	}
	if err := a.runner.Apply(ctx, runID, vars); err != nil {
		return nil, fmt.Errorf("provision: %w", err)
	}

	ips, err := a.nodePublicIPs(ctx, runID)
	if err != nil {
		return nil, err
	}
	k3sURL, err := a.runner.Output(ctx, "k3s_server_url")
	if err != nil {
		return nil, err
	}

	kubePath, err := a.fetchKubeconfig(ctx, runID)
	if err != nil {
		return nil, err
	}

	return &ProvisionOutput{
		KubeconfigPath: kubePath,
		K3sURL:         strings.TrimSpace(k3sURL),
		NodePublicIPs:  ips,
	}, nil
}

// Teardown destroys the infrastructure for the run on its isolated state key.
func (a *AWS) Teardown(ctx context.Context, runID string, job *config.Job) error {
	vars := map[string]any{
		"nodes":         job.Nodes,
		"instance_type": job.Infra.InstanceType,
		"region":        job.Infra.Region,
		"run_id":        runID,
		"ttl_minutes":   job.Infra.TTLMinutes,
		"spot":          job.Infra.Spot,
		"enable_k3s":    true,
		"state_bucket":  os.Getenv(EnvStateBucket),
		"state_region":  os.Getenv(EnvStateRegion),
		"tags":          varsTag(job),
	}
	if err := a.runner.Destroy(ctx, runID, vars); err != nil {
		return fmt.Errorf("teardown: %w", err)
	}
	// Remove the handoff objects staged during provisioning; the state file is
	// intentionally kept as run history.
	for _, key := range []string{kubeconfigS3Key, serverURLS3Key} {
		k := strings.ReplaceAll(key, "{run_id}", runID)
		_, _ = a.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(os.Getenv(EnvStateBucket)),
			Key:    aws.String(k),
		})
	}
	return nil
}

func varsTag(job *config.Job) map[string]string {
	return map[string]string{
		"openbench:job": job.Name,
	}
}

func (a *AWS) nodePublicIPs(ctx context.Context, runID string) ([]string, error) {
	raw, err := a.runner.Output(ctx, "node_public_ips")
	if err != nil {
		return nil, err
	}
	var ips []string
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal([]byte(raw), &ips); err != nil {
			return nil, fmt.Errorf("parsing node_public_ips output: %w", err)
		}
		return ips, nil
	}
	return []string{strings.TrimSpace(raw)}, nil
}

// fetchKubeconfig reads the base64-encoded kubeconfig node 0 staged in S3 and
// writes it to a temp file. It retries briefly to tolerate eventual consistency
// and the node's startup delay.
func (a *AWS) fetchKubeconfig(ctx context.Context, runID string) (string, error) {
	bucket := os.Getenv(EnvStateBucket)
	region := os.Getenv(EnvStateRegion)
	key := strings.ReplaceAll(kubeconfigS3Key, "{run_id}", runID)

	var raw string
	var err error
	deadline := time.Now().Add(120 * time.Second)
	for {
		raw, err = a.getS3(ctx, bucket, region, key)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("fetching kubeconfig from s3 (%s): %w", key, err)
		}
		time.Sleep(3 * time.Second)
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("decoding kubeconfig: %w", err)
	}

	dir := filepath.Join(os.TempDir(), "openbench", runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "k3s.yaml")
	if err := os.WriteFile(path, decoded, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (a *AWS) getS3(ctx context.Context, bucket, region, key string) (string, error) {
	if region != "" {
		if cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region)); err == nil {
			aonce := s3.NewFromConfig(cfg)
			return s3Get(ctx, aonce, bucket, key)
		}
	}
	return s3Get(ctx, a.s3, bucket, key)
}

func s3Get(ctx context.Context, cli *s3.Client, bucket, key string) (string, error) {
	out, err := cli.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return "", err
	}
	defer out.Body.Close()
	buf := new(strings.Builder)
	if _, err := io.Copy(buf, out.Body); err != nil {
		return "", err
	}
	return buf.String(), nil
}
