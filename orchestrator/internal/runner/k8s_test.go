package runner

import (
	"context"
	"github.com/TrungBui32/openbench/orchestrator/internal/secrets"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"strings"
	"testing"
	"time"

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
		`value: "full"`,
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

func TestCollectAllAndNodePlacement(t *testing.T) {
	job := &config.Job{Name: "distributed", Image: "alpine", Command: "echo hi", Nodes: 3, TimeoutMinutes: 5, OnFailure: "collect-all", Env: []config.EnvVar{{Name: "TOKEN", Value: "true: \"quoted\""}}}
	manifest, err := RenderJobManifest(job, "run-123")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := parseJob(manifest, "unused")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Spec.BackoffLimit != nil || spec.Spec.BackoffLimitPerIndex == nil || *spec.Spec.BackoffLimitPerIndex != 0 {
		t.Fatal("collect-all must fail each index independently")
	}
	rules := spec.Spec.Template.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if len(rules) != 1 || rules[0].TopologyKey != "kubernetes.io/hostname" || rules[0].LabelSelector.MatchLabels["openbench-run"] != spec.Spec.Template.Labels["openbench-run"] {
		t.Fatal("missing per-run node separation")
	}
	if spec.Spec.Template.Spec.Containers[0].Env[0].Value != job.Env[0].Value {
		t.Fatal("environment value changed")
	}
	job.OnFailure = "fail-fast"
	manifest, err = RenderJobManifest(job, "run-124")
	if err != nil {
		t.Fatal(err)
	}
	spec, err = parseJob(manifest, "unused")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Spec.BackoffLimit == nil || *spec.Spec.BackoffLimit != 0 || spec.Spec.BackoffLimitPerIndex != nil {
		t.Fatal("fail-fast must retain global failure limit")
	}
}

func TestWaitCompleteFailurePolicies(t *testing.T) {
	for _, policy := range []string{"fail-fast", "collect-all"} {
		t.Run(policy, func(t *testing.T) {
			cs := fake.NewSimpleClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-0-x", Namespace: "default", Labels: map[string]string{"job-name": "test"}}, Status: corev1.PodStatus{Phase: corev1.PodFailed}})
			polls := 0
			cs.PrependReactor("get", "jobs", func(ktesting.Action) (bool, runtime.Object, error) {
				polls++
				j := &batchv1.Job{}
				if polls >= 2 {
					j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}
				}
				return true, j, nil
			})
			k := &K8sRunner{cs: cs, ns: "default", poll: time.Millisecond, sec: secrets.New()}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := k.waitComplete(ctx, &config.Job{OnFailure: policy}, "test"); err != nil {
				t.Fatal(err)
			}
			want := 1
			if policy == "collect-all" {
				want = 2
			}
			if polls != want {
				t.Fatalf("%s stopped after %d polls, want %d", policy, polls, want)
			}
		})
	}
}

func TestLongNamesRemainUnique(t *testing.T) {
	name := strings.Repeat("long-job", 10)
	a := jobName(name, "run-abc")
	b := jobName(name, "run-def")
	if a == b || len(a) > 57 || len(b) > 57 {
		t.Fatalf("truncation lost run uniqueness: %q %q", a, b)
	}
}
