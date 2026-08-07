package runner

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/docker/docker/client"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
)

// TestDockerRunnerIntegration exercises the real Docker path. It skips when
// no daemon is reachable (e.g. CI without Docker).
func TestDockerRunnerIntegration(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("docker client unavailable: %v", err)
	}
	if _, err := cli.Ping(context.Background()); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}

	job := &config.Job{
		Name:           "integration",
		Image:          "alpine:3.20",
		Command:        "echo hello-from-docker-$HOSTNAME",
		Nodes:          2,
		TimeoutMinutes: 5,
		OnFailure:      "collect-all",
	}

	r, err := NewDockerRunner()
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	defer func() { _ = r.Close() }()

	results, err := r.Run(context.Background(), job, "integration-test")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, res := range results {
		if res.Failed() {
			t.Fatalf("node %d failed unexpectedly: %+v", res.NodeID, res)
		}
		data, err := os.ReadFile(res.LogFile)
		if err != nil {
			t.Fatalf("reading log %s: %v", res.LogFile, err)
		}
		if !strings.Contains(string(data), "hello-from-docker-") {
			t.Fatalf("log missing expected output: %q", data)
		}
	}
}

func TestDockerRunnerFailFast(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("docker client unavailable: %v", err)
	}
	if _, err := cli.Ping(context.Background()); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}

	job := &config.Job{
		Name:           "integration",
		Image:          "alpine:3.20",
		Command:        "sh -c 'echo fail; exit 1'",
		Nodes:          2,
		TimeoutMinutes: 5,
		OnFailure:      "fail-fast",
	}

	r, err := NewDockerRunner()
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	defer func() { _ = r.Close() }()

	results, err := r.Run(context.Background(), job, "integration-failfast")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	failed := 0
	for _, res := range results {
		if res.Failed() {
			failed++
		}
	}
	if failed == 0 {
		t.Fatal("expected at least one failed node")
	}
}
