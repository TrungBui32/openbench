package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
	"github.com/TrungBui32/openbench/orchestrator/internal/secrets"
)

// DockerRunner executes a job locally with N parallel containers. Each
// container's combined stdout/stderr is captured to a temp file on the
// orchestrator host; the storage layer uploads those files afterwards.
type DockerRunner struct {
	cli *client.Client
	sec *secrets.Resolver
}

func NewDockerRunner() (*DockerRunner, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &DockerRunner{cli: cli, sec: secrets.New()}, nil
}

func (d *DockerRunner) Close() error { return d.cli.Close() }

// Run launches one container per node in parallel. Under fail-fast, the first
// node failure cancels the others (in-flight containers are killed).
func (d *DockerRunner) Run(ctx context.Context, job *config.Job, runID string) ([]Result, error) {
	resolved, err := d.sec.Resolve(job.Secrets)
	if err != nil {
		return nil, err
	}
	env := envPairs(job, resolved)

	if err := d.ensureImage(ctx, job.Image); err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]Result, job.Nodes)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < job.Nodes; i++ {
		wg.Add(1)
		go func(nodeID int) {
			defer wg.Done()
			res, err := d.runNode(runCtx, job, runID, env, nodeID)
			if err != nil {
				res.Err = err
			}
			mu.Lock()
			results[nodeID] = res
			if job.OnFailure == "fail-fast" && res.Failed() {
				cancel()
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	return results, nil
}

func (d *DockerRunner) runNode(ctx context.Context, job *config.Job, runID string, env []string, nodeID int) (Result, error) {
	res := Result{NodeID: nodeID}

	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("node %d skipped: %w", nodeID, err)
	}

	name := fmt.Sprintf("openbench-%s-%d", jobName(job.Name, runID), nodeID)
	body, err := d.cli.ContainerCreate(ctx, &container.Config{
		Image: job.Image,
		Cmd:   []string{"/bin/sh", "-c", job.Command},
		Env:   env,
		Labels: map[string]string{
			"openbench.job":  job.Name,
			"openbench.run":  runID,
			"openbench.node": fmt.Sprintf("%d", nodeID),
		},
	}, nil, nil, nil, name)
	if err != nil {
		return res, fmt.Errorf("creating container: %w", err)
	}
	// Ensure the container is always removed, even on start/wait/timeout errors.
	defer func() {
		_ = d.cli.ContainerRemove(context.Background(), body.ID, container.RemoveOptions{Force: true})
	}()

	timeout := time.Duration(job.TimeoutMinutes) * time.Minute
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := d.cli.ContainerStart(runCtx, body.ID, container.StartOptions{}); err != nil {
		return res, fmt.Errorf("starting container: %w", err)
	}

	waitCtx, waitCancel := context.WithCancel(runCtx)
	defer waitCancel()
	waitCh, errCh := d.cli.ContainerWait(waitCtx, body.ID, container.WaitConditionNotRunning)

	select {
	case <-runCtx.Done():
		_ = d.cli.ContainerKill(context.Background(), body.ID, "SIGKILL")
		return res, fmt.Errorf("node %d timed out after %s", nodeID, timeout)
	case err := <-errCh:
		if err != nil {
			return res, fmt.Errorf("waiting for container: %w", err)
		}
	case <-waitCh:
	}

	info, err := d.cli.ContainerInspect(context.Background(), body.ID)
	if err != nil {
		return res, fmt.Errorf("inspecting container: %w", err)
	}
	res.ExitCode = info.State.ExitCode

	logFile, err := os.CreateTemp("", "openbench-node-*.log")
	if err != nil {
		return res, fmt.Errorf("creating log file: %w", err)
	}
	defer logFile.Close()
	res.LogFile = logFile.Name()

	out, err := d.cli.ContainerLogs(context.Background(), body.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err == nil {
		defer out.Close()
		// StdCopy demuxes the multiplexed stream; writing both stdout and stderr
		// to the same writer merges them into one combined log file.
		_, _ = stdcopy.StdCopy(logFile, logFile, out)
	}

	return res, nil
}

func (d *DockerRunner) ensureImage(ctx context.Context, ref string) error {
	if _, err := d.cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	reader, err := d.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", ref, err)
	}
	defer reader.Close()
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return fmt.Errorf("pulling image %s: %w", ref, err)
	}
	return nil
}

func envPairs(job *config.Job, secrets map[string]string) []string {
	// Deterministic dedupe: secrets override matching plain env vars, then the
	// result is sorted so the emitted list is stable across runs.
	merged := make(map[string]string, len(job.Env)+len(secrets))
	for _, e := range job.Env {
		merged[e.Name] = e.Value
	}
	for k, v := range secrets {
		merged[k] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+merged[k])
	}
	return env
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			out = append(out, r)
		} else {
			out = append(out, '-')
		}
	}
	return string(out)
}
