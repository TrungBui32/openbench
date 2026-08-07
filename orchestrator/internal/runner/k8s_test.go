package runner

import (
	"strings"
	"testing"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
)

func TestRenderJobManifest(t *testing.T) {
	job := &config.Job{
		Name:    "gpu-driver-regression",
		Image:   "myrepo/gpu-test:latest",
		Command: "./run_test.sh --suite=full",
		Nodes:   5,
		Env:     []config.EnvVar{{Name: "TEST_MODE", Value: "full"}},
	}

	out, err := RenderJobManifest(job, "gpu-driver-regression-20260803-0142")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, want := range []string{
		"kind: Job",
		"parallelism: 5",
		"completions: 5",
		"backoffLimit: 0",
		"restartPolicy: Never",
		"image: myrepo/gpu-test:latest",
		`"./run_test.sh --suite=full"`,
		"name: TEST_MODE",
		"value: full",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("manifest missing %q:\n%s", want, out)
		}
	}
}

func TestPodOrdinal(t *testing.T) {
	cases := []struct {
		name, job string
		want      int
		wantErr   bool
	}{
		{"dbg-0-9df2c", "dbg", 0, false},
		{"dbg-1-cv99l", "dbg", 1, false},
		{"dbg-2-lxb7z", "dbg", 2, false},
		{"app-run-3-abc", "app-run", 3, false},
		{"app-run-4", "app-run", 4, false},
		{"nope", "dbg", 0, true},
		{"dbg-x-abc", "dbg", 0, true},
	}
	for _, c := range cases {
		got, err := podOrdinal(c.name, c.job)
		if c.wantErr {
			if err == nil {
				t.Errorf("podOrdinal(%s,%s) expected error, got %d", c.name, c.job, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("podOrdinal(%s,%s) unexpected error: %v", c.name, c.job, err)
			continue
		}
		if got != c.want {
			t.Errorf("podOrdinal(%s,%s) = %d, want %d", c.name, c.job, got, c.want)
		}
	}
}
