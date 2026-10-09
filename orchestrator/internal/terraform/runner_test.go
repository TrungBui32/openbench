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

func TestPrivateWorkingDirectories(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(source+"/terraform", []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", source)
	if err := os.WriteFile(source+"/main.tf", []byte("terraform {}"), 0600); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(source+"/.terraform", 0700)
	os.WriteFile(source+"/.terraform/terraform.tfstate", []byte("private"), 0600)
	os.WriteFile(source+"/terraform.tfstate", []byte("private"), 0600)
	a, err := New(source)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := New(source)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if a.Dir() == b.Dir() || a.Dir() == source {
		t.Fatal("working directories not isolated")
	}
	if _, err := os.Stat(a.Dir() + "/main.tf"); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"terraform.tfstate", ".terraform"} {
		if _, err := os.Stat(a.Dir() + "/" + file); !os.IsNotExist(err) {
			t.Fatalf("copied backend state %s", file)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.Dir() + "/main.tf"); err != nil {
		t.Fatal("closing one runner affected another", err)
	}
}
