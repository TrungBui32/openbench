package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "embed"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

	"github.com/TrungBui32/openbench/orchestrator/internal/config"
	"github.com/TrungBui32/openbench/orchestrator/internal/secrets"
)

//go:embed test-job.yaml.tmpl
var jobManifestTemplate string

// K8sRunner schedules a Kubernetes Job with parallelism N and collects each
// pod's exit code and logs via the API. Logs are fetched through the API
// rather than sidecars so the orchestrator has a single dependency.
type K8sRunner struct {
	cs   kubernetes.Interface
	ns   string
	poll time.Duration
	sec  *secrets.Resolver
}

// NewK8sRunner builds a runner against the default kubeconfig resolution
// (in-cluster config, then $KUBECONFIG, then ~/.kube/config).
func NewK8sRunner() (*K8sRunner, error) {
	return newK8sRunner("")
}

// NewK8sRunnerWithConfig builds a runner pinned to an explicit kubeconfig file
// (e.g. one staged during k3s provisioning).
func NewK8sRunnerWithConfig(kubeconfigPath string) (*K8sRunner, error) {
	return newK8sRunner(kubeconfigPath)
}

func newK8sRunner(kubeconfigPath string) (*K8sRunner, error) {
	cfg, err := kubeConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building kubernetes client: %w", err)
	}
	return &K8sRunner{cs: cs, ns: "default", poll: 2 * time.Second, sec: secrets.New()}, nil
}

func kubeConfig(path string) (*rest.Config, error) {
	if path != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			return nil, fmt.Errorf("loading kubeconfig %s: %w", path, err)
		}
		return cfg, nil
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig %s: %w", kubeconfig, err)
	}
	return cfg, nil
}

// Run renders and applies an indexed Job (completionMode: Indexed) so pods map
// deterministically to node IDs via their ordinal suffix, then waits for it to
// finish (respecting job.TimeoutMinutes) and collects exit codes + logs.
//
// Per-node timeout is enforced by activeDeadlineSeconds on the pod template; a
// timed-out node becomes a failed pod (backoffLimit 0 fails the job). With
// fail-fast, the first failed pod aborts the wait so the remaining in-flight
// pods are reported as skipped rather than run to completion.
func (k *K8sRunner) Run(ctx context.Context, job *config.Job, runID string) ([]Result, error) {
	resolved, err := k.sec.Resolve(job.Secrets)
	if err != nil {
		return nil, err
	}
	manifest, err := renderJobManifest(job, runID, mergedEnv(job.Env, resolved))
	if err != nil {
		return nil, err
	}
name := jobName(job.Name, runID)
	jobSpec, err := parseJob(manifest, name)
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(job.TimeoutMinutes) * time.Minute
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wait for the requested number of nodes to be Ready before scheduling, so
	// in a multi-node run the job's pods actually spread across the server and
	// its workers instead of stacking on node 0. Bounded by the job timeout.
	if err := k.waitForReadyNodes(runCtx, job.Nodes); err != nil {
		return nil, err
	}

	namespaced := k.cs.BatchV1().Jobs(k.ns)
	if _, err := namespaced.Create(runCtx, jobSpec, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("creating job: %w", err)
	}
	defer func() {
		_ = namespaced.Delete(context.Background(), name, metav1.DeleteOptions{PropagationPolicy: ptr(metav1.DeletePropagationForeground)})
	}()

	if err := k.waitComplete(runCtx, job, name); err != nil {
		if results, cerr := k.collectResults(context.Background(), job, name); cerr == nil {
			return results, nil
		}
		return nil, err
	}

	return k.collectResults(runCtx, job, name)
}

// waitForReadyNodes polls the cluster until at least want nodes are Ready, so
// multi-node runs don't start before their workers have joined.
func (k *K8sRunner) waitForReadyNodes(ctx context.Context, want int) error {
	return wait.PollUntilContextCancel(ctx, k.poll, true, func(ctx context.Context) (bool, error) {
		nodes, err := k.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, nil // transient; keep polling
		}
		ready := 0
		for _, n := range nodes.Items {
			for _, c := range n.Status.Conditions {
				if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
					ready++
					break
				}
			}
		}
		return ready >= want, nil
	})
}

// waitComplete polls the Job until it is Complete or Failed (backoffLimit 0).
// With fail-fast it also watches pod state directly: once any pod fails while
// others are still running, it aborts the wait early so in-flight nodes are
// reported as skipped.
func (k *K8sRunner) waitComplete(ctx context.Context, job *config.Job, jobName string) error {
	failFast := job.OnFailure == "fail-fast"
	err := wait.PollUntilContextCancel(ctx, k.poll, true, func(ctx context.Context) (bool, error) {
		j, err := k.cs.BatchV1().Jobs(k.ns).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, c := range j.Status.Conditions {
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				return true, nil
			}
			if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
				// backoffLimit is 0: a failed pod means the job failed. We still
				// collect results below, so return "complete" and let
				// collectResults report per-node failures.
				return true, nil
			}
		}
		if failFast && k.anyPodFailed(ctx, jobName) {
			return true, nil
		}
		return false, nil
	})
	return err
}

// anyPodFailed reports whether any pod of the job is in a Failed phase.
func (k *K8sRunner) anyPodFailed(ctx context.Context, jobName string) bool {
	pods, err := k.cs.CoreV1().Pods(k.ns).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + jobName})
	if err != nil {
		return false
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodFailed {
			return true
		}
	}
	return false
}

// collectResults maps each pod back to a node index via its ordinal suffix.
// Pods that never reached a terminal state (still running when fail-fast or the
// job-level timeout fired) are reported as skipped with an Err, mirroring the
// DockerRunner's cancellation semantics.
func (k *K8sRunner) collectResults(ctx context.Context, job *config.Job, jobName string) ([]Result, error) {
	pods, err := k.cs.CoreV1().Pods(k.ns).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + jobName})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	results := make([]Result, job.Nodes)
	for i := range results {
		results[i] = Result{NodeID: i, ExitCode: 1}
	}

	for _, pod := range pods.Items {
		idx, err := podOrdinal(pod.Name, jobName)
		if err != nil || idx < 0 || idx >= job.Nodes {
			continue
		}
		code := 1
		terminated := false
		if cs := pod.Status.ContainerStatuses; len(cs) > 0 {
			if t := cs[0].State.Terminated; t != nil {
				code = int(t.ExitCode)
				terminated = true
			}
		}
		if !terminated {
			results[idx] = Result{NodeID: idx, Err: fmt.Errorf("node %d skipped: job aborted before it finished", idx)}
			continue
		}

		logPath, logErr := k.podLogFile(ctx, jobName, pod.Name)
		results[idx] = Result{NodeID: idx, ExitCode: code, LogFile: logPath}
		if logErr != nil {
			results[idx].Err = logErr
		}
	}
	return results, nil
}

func (k *K8sRunner) podLogFile(ctx context.Context, jobName, podName string) (string, error) {
	req := k.cs.CoreV1().Pods(k.ns).GetLogs(podName, &corev1.PodLogOptions{Container: "test"})
	rc, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("streaming logs for %s: %w", podName, err)
	}
	defer rc.Close()

	f, err := os.CreateTemp("", "openbench-k8s-*.log")
	if err != nil {
		return "", fmt.Errorf("creating log file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, rc); err != nil {
		return "", fmt.Errorf("copying logs for %s: %w", podName, err)
	}
	return f.Name(), nil
}

// RenderJobManifest renders the Kubernetes Job manifest (Go text/template)
// for a job and run with the job's plain env vars. Used by dry-run (secrets
// are intentionally not materialized without a run).
func RenderJobManifest(job *config.Job, runID string) (string, error) {
	return renderJobManifest(job, runID, job.Env)
}

// renderJobManifest renders the Job manifest with the full env list (plain
// env vars plus resolved secrets) for an actual run.
func renderJobManifest(job *config.Job, runID string, env []config.EnvVar) (string, error) {
	data := struct {
		Name           string
		RunID          string
		Image          string
		Command        string
		Nodes          int
		TimeoutSeconds int64
		Env            []config.EnvVar
	}{
		Name:           job.Name,
		RunID:          sanitize(runID),
		Image:          job.Image,
		Command:        job.Command,
		Nodes:          job.Nodes,
		TimeoutSeconds: int64(job.TimeoutMinutes * 60),
		Env:            env,
	}
	return executeTemplate(jobManifestTemplate, data)
}

// mergedEnv combines plain env vars with resolved secrets, secrets winning on
// name collisions. The slice is sorted for deterministic rendering.
func mergedEnv(base []config.EnvVar, secrets map[string]string) []config.EnvVar {
	byName := make(map[string]string, len(base)+len(secrets))
	for _, e := range base {
		byName[e.Name] = e.Value
	}
	for k, v := range secrets {
		byName[k] = v
	}
	out := make([]config.EnvVar, 0, len(byName))
	for k, v := range byName {
		out = append(out, config.EnvVar{Name: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func parseJob(manifest, fallbackName string) (*batchv1.Job, error) {
	var job batchv1.Job
	if err := yaml.Unmarshal([]byte(manifest), &job); err != nil {
		return nil, fmt.Errorf("parsing rendered manifest: %w", err)
	}
	if job.Name == "" {
		job.Name = fallbackName
	}
	if job.Spec.BackoffLimit == nil {
		job.Spec.BackoffLimit = ptr(int32(0))
	}
	return &job, nil
}

func podOrdinal(podName, jobName string) (int, error) {
	idx := strings.TrimPrefix(podName, jobName+"-")
	if i := strings.IndexByte(idx, '-'); i >= 0 {
		// Indexed job pod names carry a random suffix (pod-0-abc12), so stop
		// at the ordinal and ignore everything after it.
		idx = idx[:i]
	}
	return strconv.Atoi(idx)
}

func ptr[T any](v T) *T { return &v }
