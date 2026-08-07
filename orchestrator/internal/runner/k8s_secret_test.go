package runner

import (
	"strings"
	"testing"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
)

func TestRenderJobManifestIncludesResolvedSecrets(t *testing.T) {
	job := &config.Job{
		Name:    "secret-consumer",
		Image:   "myrepo/consumer:latest",
		Command: "./run.sh",
		Nodes:   1,
		Env:     []config.EnvVar{{Name: "PLAIN", Value: "value"}},
	}

	out, err := renderJobManifest(job, "run-1", mergedEnv(job.Env, map[string]string{
		"SECRET_A": "alpha",
		"SECRET_B": "beta",
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, want := range []string{
		"name: PLAIN",
		"value: value",
		"name: SECRET_A",
		"value: alpha",
		"name: SECRET_B",
		"value: beta",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("manifest missing %q:\n%s", want, out)
		}
	}
}

func TestMergedEnvSecretsOverridePlain(t *testing.T) {
	got := mergedEnv(
		[]config.EnvVar{{Name: "K", Value: "plain"}, {Name: "A", Value: "a"}},
		map[string]string{"K": "secret"},
	)

	if len(got) != 2 {
		t.Fatalf("want 2 vars, got %d: %+v", len(got), got)
	}
	if got[0].Name != "A" {
		t.Errorf("expected sorted first var A, got %+v", got[0])
	}
	for _, v := range got {
		if v.Name == "K" && v.Value != "secret" {
			t.Errorf("secret should win: %+v", v)
		}
	}
}
