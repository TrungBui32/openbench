# OpenBench Configuration Reference

One `job.yaml` describes an entire test run. The orchestrator validates it
against a JSON Schema (`config/job.schema.json`) plus semantic rules before
doing anything.

## Schema

```yaml
job:
  name: gpu-driver-regression        # unique job identifier (^[a-z0-9][a-z0-9-]*$)
  image: myrepo/gpu-test:latest      # docker image to run
  command: "./run_test.sh --suite=full"   # overrides the image entrypoint
  nodes: 5                           # parallel replicas (>= 1)
  timeout_minutes: 45                # per-job timeout; must be <= infra.ttl_minutes

  on_failure: fail-fast              # fail-fast | collect-all (default fail-fast)
                                     # fail-fast: stop/report as soon as one node fails
                                     # collect-all: let all N nodes finish, then report

  env:                               # plain env vars injected into every container
    - name: TEST_MODE
      value: full

  secrets:                           # secrets injected into every container
    - name: REGISTRY_TOKEN
      source: env                    # env | jenkins-credentials
      key: OPENBENCH_REGISTRY_TOKEN  # env var name / Jenkins credential ID

  infra:
    provider: aws                    # aws only
    instance_type: t3.xlarge         # must be in the supported instance-type map
    region: ap-southeast-2
    spot: true                       # launch nodes as EC2 spot (cheaper; interruptible)
    ttl_minutes: 60                  # safety auto-teardown; hard upper bound on job lifetime

  storage:
    type: s3                         # local | s3
    # local requires: dir
    # s3 requires:   bucket
    dir: /var/openbench/logs         # local-only
    bucket: my-test-logs             # s3-only
    path_prefix: gpu-driver/{run_id}/   # {run_id} is substituted at run time

  trigger:
    type: manual                     # manual | jenkins-webhook | cron
    cron_schedule: null              # required (and validated) when type == cron
```

## Validation rules

Structural (JSON Schema `config/job.schema.json`):

- `storage.type` selects the field matrix: `local` → `dir` required; `s3` → `bucket` required.
- `nodes >= 1`; `name` matches `[a-z0-9][a-z0-9-]*`.
- `trigger.type == cron` requires a `cron_schedule`.

Semantic (enforced by the orchestrator):

- `timeout_minutes <= infra.ttl_minutes` — a job never outlives its own safety net.
- `infra.instance_type` must be in the supported map (bounded cost exposure).
- `infra.spot` (bool, optional) marks nodes as EC2 spot. Use only when the test
  tolerates interruption; the k3s control-plane node is itself spot (see note
  under instance types).
- `infra.provider` must be `aws`.
- `storage.path_prefix` is sanitized against path traversal (no `..`, no absolute paths, only `[a-zA-Z0-9/_\-{}]`).
- `trigger.cron_schedule` must parse as a valid cron expression.

## Storage backends

| type | fields | notes |
|---|---|---|
| `local` | `dir` | Files written under `dir/<path_prefix>/{run_id}/` |
| `s3` | `bucket` | Objects keyed `s3://<bucket>/<path_prefix>/{run_id}/`; credentials via the standard AWS chain |

Every run writes:

- `<path_prefix>/{run_id}/node-{n}/stdout.log` per node
- `<path_prefix>/{run_id}/summary.json` — the structured run summary, the artifact Jenkins parses for metrics.

## Supported instance types

`t3.small`, `t3.medium`, `t3.large`, `t3.xlarge`, `t2.small`, `t2.medium`,
`m5.large`, `m5.xlarge`, `c5.large`, `c5.xlarge`, `g4dn.xlarge`, `g4dn.2xlarge`.

`g4dn.*` carry an NVIDIA T4 GPU; the rest are CPU-only. Instances get an 8 GiB
root volume (the Ubuntu 22.04 AMI default) — if a workload needs more disk, add
a `root_block_device` to `terraform/modules/aws-test-nodes/main.tf`.

With `spot: true`, **every** node (including node 0, the k3s server) is a spot
instance. A reclaimed server node aborts the run mid-test; for long or
uninterruptible runs prefer on-demand, or keep node 0 on-demand (only workers
spot).

## Secrets resolution

- `source: env` — read from the orchestrator's own environment at run time.
- `source: jenkins-credentials` — resolved by the Jenkins pipeline before the CLI is invoked; the credential value is injected as an env var named by `key`.

## Remote Terraform state

Per-run state keys are isolated in S3. Configure via env vars on the
orchestrator: `OPENBENCH_STATE_BUCKET`, `OPENBENCH_STATE_REGION`,
`OPENBENCH_STATE_LOCK_TABLE`. The terraform root is located via
`OPENBENCH_TERRAFORM_DIR` (default `./terraform`). A job whose `infra.provider`
is `aws` runs Docker-style `--mode k8s` against a provisioned k3s cluster: the
terraform config is applied, the node stages its kubeconfig into the state
bucket for the orchestrator to fetch, and teardown is attempted after execution.
Cleanup failures appear in `status`
and return a nonzero exit code; use `destroy` to retry.
