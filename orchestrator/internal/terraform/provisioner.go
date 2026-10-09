package terraform

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

// bootstrapTokenS3Key stores the server-generated private join token.
const bootstrapTokenS3Key = "kubeconfig/{run_id}/join-token"

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
		_ = r.Close()
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

// Close removes the private Terraform working directory.
func (a *AWS) Close() error { return a.runner.Close() }

// Teardown destroys resources and removes credential handoffs, reporting failures.
func (a *AWS) Teardown(ctx context.Context, runID string, job *config.Job) error {
	defer os.RemoveAll(filepath.Join(os.TempDir(), "openbench", runID))
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
	var cleanupErr error
	if err := a.runner.Destroy(ctx, runID, vars); err != nil {
		cleanupErr = fmt.Errorf("teardown: %w", err)
	}
	// Delete all versions of credential handoffs, even after a failed destroy.
	for _, key := range []string{kubeconfigS3Key, serverURLS3Key, bootstrapTokenS3Key} {
		k := strings.ReplaceAll(key, "{run_id}", runID)
		cleanupErr = errors.Join(cleanupErr, a.deleteHandoff(ctx, k))
	}
	return cleanupErr
}

func (a *AWS) deleteHandoff(ctx context.Context, key string) error {
	bucket := aws.String(os.Getenv(EnvStateBucket))
	options := func(o *s3.Options) { o.Region = os.Getenv(EnvStateRegion) }
	pages := s3.NewListObjectVersionsPaginator(a.s3, &s3.ListObjectVersionsInput{Bucket: bucket, Prefix: aws.String(key)})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx, options)
		if err != nil {
			return fmt.Errorf("listing bootstrap versions %s: %w", key, err)
		}
		for _, version := range page.Versions {
			if aws.ToString(version.Key) != key {
				continue
			}
			if _, err := a.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: bucket, Key: version.Key, VersionId: version.VersionId}, options); err != nil {
				return err
			}
		}
		for _, marker := range page.DeleteMarkers {
			if aws.ToString(marker.Key) != key {
				continue
			}
			if _, err := a.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: bucket, Key: marker.Key, VersionId: marker.VersionId}, options); err != nil {
				return err
			}
		}
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
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
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
