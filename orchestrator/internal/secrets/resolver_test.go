package secrets

import (
	"testing"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
)

func TestResolveFromEnv(t *testing.T) {
	t.Setenv("OPENBENCH_TEST_TOKEN", "sekret")
	r := New()
	out, err := r.Resolve([]config.Secret{{Name: "REGISTRY_TOKEN", Source: "env", Key: "OPENBENCH_TEST_TOKEN"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if out["REGISTRY_TOKEN"] != "sekret" {
		t.Fatalf("unexpected value: %q", out["REGISTRY_TOKEN"])
	}
}

func TestResolveMissingEnv(t *testing.T) {
	r := New()
	_, err := r.Resolve([]config.Secret{{Name: "TOKEN", Source: "env", Key: "OPENBENCH_DEFINITELY_NOT_SET"}})
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
}

func TestResolveJenkinsCredentialsRequiresPreseededValue(t *testing.T) {
	t.Setenv("registry-token", "from-jenkins")
	r := New()
	out, err := r.Resolve([]config.Secret{{Name: "TOKEN", Source: "jenkins-credentials", Key: "registry-token"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if out["TOKEN"] != "from-jenkins" {
		t.Fatalf("unexpected value: %q", out["TOKEN"])
	}
}

func TestResolveUnknownSource(t *testing.T) {
	r := New()
	_, err := r.Resolve([]config.Secret{{Name: "TOKEN", Source: "vault", Key: "x"}})
	if err == nil {
		t.Fatal("expected error for unknown source")
	}
}
