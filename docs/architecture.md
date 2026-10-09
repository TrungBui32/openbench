# OpenBench Architecture

OpenBench is a control-plane service that ties existing tools together behind
one config file. The engineer's only interface is `job.yaml`; everything else —
provisioning, scheduling, log shipping, teardown — is automatic.

```
Engineer writes job.yaml
        │  invokes
        ▼
   Orchestrator (Go CLI)
        ├── Runner interface ─── DockerRunner / K8sRunner
        ├── Terraform ────────── terraform-exec → AWS EC2 + k3s
        ├── StorageBackend ───── local | s3
        └── TTL watcher ──────── independent teardown of expired infra
```

## Components

### Orchestrator (`orchestrator/`)

The core product. Responsibilities:

1. Load and validate `job.yaml` (JSON Schema + semantic rules).
2. Generate a unique `run_id`.
3. Execute the job through a `Runner` (`internal/runner`).
4. Collect logs and upload them through a `StorageBackend` (`internal/storage`).
5. Emit the structured run summary (`pkg/runresult`), written as
   `<path_prefix>/{run_id}/summary.json`.
6. Persist lightweight run metadata (`internal/runstore`) for
   `status` / `logs` / `destroy`.

### Runner (`internal/runner`)

A `Runner` interface abstracts execution so the local Docker path and the
Kubernetes path are interchangeable.

- **DockerRunner** runs N parallel containers locally, captures combined
  stdout/stderr per node, and returns exit codes. Supports `fail-fast`
  (cancel others on first failure) and per-node timeouts.
- **K8sRunner** renders an indexed `Job` manifest (`parallelism: N`,
  `completionMode: Indexed`, `restartPolicy: Never`) via
  client-go, waits for completion, then collects per-pod exit codes and logs.

### Storage (`internal/storage`)

A `Backend` interface (`Upload`/`Write`/`Read`) with `local` and `s3`
implementations. Adding a backend means one new file, not orchestrator changes.

### Terraform (`terraform/`, `internal/terraform`)

`terraform/modules/aws-test-nodes` provisions N EC2 instances (Docker + optional
k3s bootstrap). State is remote (S3 + DynamoDB lock) with a **per-run state
key** (`state/{run_id}/terraform.tfstate`). Each runner uses a private working
directory so concurrent runs cannot
overwrite one another’s local backend configuration. Resources are tagged
`openbench:run_id` and `openbench:ttl_expires_at`.

AWS provisioning is wired into the lifecycle (`orchestrator/orchestrator.go`):
when a job declares `infra.provider: aws`, `run` **provisions** first (forcing
`enable_k3s=true`), hands the resulting k3s kubeconfig to a `K8sRunner`, then
**attempts teardown** in a deferred call — even on test
failures or partial provisioning. Cleanup failures return an error and persist
in the run record for `status`; retry with `destroy`. `destroy` also runs
the teardown against the
run's state key before dropping local metadata.

### k3s cluster handoff

After apply, node 0 rewrites its kubeconfig `server:` to its public IP and
stages it base64-encoded in the state bucket (`kubeconfig/{run_id}/`). The
orchestrator downloads it (`internal/terraform/provisioner.go`), writes it to a
temp file, and passes the path to `NewK8sRunnerWithConfig`. No SSH is needed
for the control path.

### TTL watcher (`internal/ttlwatcher`)

An independent process (`openbench ttl-watch`) that scans EC2 instances by the
`ttl_expires_at` tag and force-terminates anything past its TTL, regardless of
whether the orchestrator process is alive. This is the crash-safety net for the
ephemeral-infrastructure guarantee.

### Jenkins (`jenkins/Jenkinsfile`)

The CI entrypoint. Builds the orchestrator, validates the config, runs the job,
and surfaces the exit code as build status. The run summary written to the
storage backend can be parsed for pass/fail counts and pushed to Prometheus.

## Pass/fail semantics

A node's outcome is its container exit code (`0` = pass). Run status is
`passed` when all nodes pass; `failed` when any node fails. `on_failure`
controls whether remaining nodes are cancelled on the first failure
(`fail-fast`) or all finish first (`collect-all`).

## Path conventions

- Logs: `<path_prefix>/{run_id}/node-{node_id}/stdout.log`
- Summary: `<path_prefix>/{run_id}/summary.json`

## Metrics (planned, deferred)

Jenkins parses `summary.json` → Prometheus Pushgateway → Grafana. The summary
schema is stable, so wiring this in needs no schema changes.

## Failure isolation and bootstrap

Collect-all uses `backoffLimitPerIndex: 0` (Kubernetes 1.33+ or the enabled
feature gate): failed indexes do not cancel the remaining indexes. Fail-fast
uses `backoffLimit: 0`. Required pod anti-affinity spreads each run across
separate hostnames; a run needs N schedulable nodes.

Node 0 generates a random 256-bit join token and shares it with workers through
this run's IAM-scoped S3 prefix. It is never placed in Terraform variables or
state. Teardown deletes every version of the token and kubeconfig handoffs and
removes the local kubeconfig. The operator needs `s3:ListBucketVersions` and
`s3:DeleteObjectVersion` in addition to the existing handoff permissions.
The TTL watcher terminates expired instances; retry `destroy` to remove other
resources after a crash. Cleanup cannot be guaranteed during API outages.
