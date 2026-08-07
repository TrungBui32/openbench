package terraform

import (
	"os"
	"testing"
)

func TestHCLValue(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hello", `"hello"`},
		{true, "true"},
		{false, "false"},
		{5, "5"},
		{[]string{"a", "b"}, `["a", "b"]`},
		{map[string]string{"b": "2", "a": "1"}, `{"a" = "1", "b" = "2"}`},
		{map[string]string{"openbench:job": "hello-k8s"}, `{"openbench:job" = "hello-k8s"}`},
	}
	for _, c := range cases {
		if got := hclValue(c.in); got != c.want {
			t.Errorf("hclValue(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStateBackendConfigRequiresEnv(t *testing.T) {
	for _, k := range []string{EnvStateBucket, EnvStateRegion, EnvStateLock} {
		os.Unsetenv(k)
	}
	if _, err := stateBackendConfig("run-1"); err == nil {
		t.Fatal("expected error when state backend env is missing")
	}
}
