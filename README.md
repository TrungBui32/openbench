# OpenBench

Automated test orchestration: define a test job in **one YAML file**, and the
platform provisions compute, runs the test in parallel across N nodes,
collects logs, stores them where you choose, and tears everything down.

> **The pitch:** one config file in, fully executed and logged test run out.

## Prerequisites

- **Go 1.25+** — to build the CLI.
- **Docker** — for local (`--mode docker`) runs.
- **AWS / k3s runs** additionally need: `aws` CLI v2 with credentials,
  `terraform` (>= 1.5), and `kubectl`. The account must have a default VPC in
  the job's `infra.region`.

## Quickstart — local Docker

```bash
cd orchestrator
go build -o openbench ./cmd/openbench
cd ..

./orchestrator/openbench validate config/examples/example-job-local.yaml
./orchestrator/openbench dry-run  config/examples/example-job-local.yaml
./orchestrator/openbench run      config/examples/example-job-local.yaml
./orchestrator/openbench status   <run_id>
./orchestrator/openbench logs     <run_id> [--node 0]
```

Requires a reachable Docker daemon. Logs land under
`storage.dir/hello-local/{run_id}/node-{n}/` with a `summary.json` alongside.

## CLI

| Command | Purpose |
|---|---|
| `openbench validate <job.yaml>` | Schema + semantic validation, no side effects |
| `openbench dry-run <job.yaml>` | Print the full execution plan without acting |
| `openbench run <job.yaml> [--mode docker\|k8s]` | Full lifecycle: provision (if `infra.provider=aws`) → test → log → teardown; returns a `run_id` |
| `openbench status <run_id>` | Current phase and outcome of a run |
| `openbench logs <run_id> [--node N]` | Print collected logs |
| `openbench destroy <run_id>` | Force immediate teardown of a run's infrastructure |
| `openbench ttl-watch [--once]` | Crash-safety net: terminate AWS instances past their TTL |

## Quickstart — AWS (k3s)

One-time AWS setup is scripted. Run it once with valid credentials:

```bash
./scripts/verify-aws.sh        # creates the state bucket + DynamoDB lock table + logs bucket,
                               # then prints the env vars to export
```

Then build and run an example job against a real cluster:

```bash
# from a shell with the printed exports set, plus OPENBENCH_TERRAFORM_DIR=./terraform
export OPENBENCH_TERRAFORM_DIR=./terraform
./orchestrator/openbench run --mode k8s config/examples/example-job-aws-k8s.yaml
./orchestrator/openbench status <run_id>
./orchestrator/openbench logs   <run_id>
./orchestrator/openbench destroy <run_id>   # tear down the provisioned nodes
```

`run --mode k8s` provisions EC2 nodes (optionally as spot via `infra.spot`),
bootstraps a k3s cluster — node 0 is the control plane and the rest join as
workers — schedules the job across the cluster, collects logs into S3, and
attempts teardown of the cluster and staged handoff objects when the run ends.
Kubernetes collect-all requires Kubernetes 1.33+ (or a cluster with
`JobBackoffLimitPerIndex` enabled). Pods are required to run on distinct nodes.
Point the example's `storage.bucket` at the logs bucket the verify script
reports.

## How a run works

`openbench run <job.yaml>` is one lifecycle, guided by the job's `infra` and
`storage` blocks:

1. **Provision** — if `infra.provider: aws`, apply the Terraform config
   (`terraform/`) to a per-run state key: security group (k3s API + inter-node),
   IAM role, and N EC2 nodes. Node 0 bootstraps the **k3s server**; every other
   node joins as a **worker** via an IAM-scoped S3 handoff with a random
   server-generated join token (no SSH keys are created or stored). The server stages its kubeconfig in S3 for the
   orchestrator to fetch. `infra.spot` launches nodes as spot instead of
   on-demand.
2. **Test** — a `--mode docker` run fans out N containers on the host; a
   `--mode k8s` run schedules an indexed Kubernetes Job (parallelism N) on the
   provisioned cluster. Fail-fast aborts on the first failed node; collect-all
   waits for all N.
3. **Collect** — per-node logs and a `summary.json` (the Jenkins-parsed metric
   artifact) are written to local storage or S3.
4. **Teardown** — the cluster computes, S3-staged kubeconfig, and Terraform
   resources are scheduled for teardown, including after failed runs. Cleanup
   failures return a nonzero exit code and remain visible in `status` for retry.
   `openbench
   destroy <run_id>` forces the same teardown on demand; `openbench ttl-watch`
   is the crash-safety net that terminates instances past their TTL.

## Project layout

```
config/            job schemas + example jobs
docs/              configuration reference + architecture
orchestrator/      the Go CLI & engine (cmd/openbench, internal/*)
terraform/         AWS provisioning (root config + test-node module)
scripts/           verify-aws.sh (one-time AWS setup)
jenkins/           Jenkinsfile entrypoint
k8s/               Kubernetes manifests/templates
```

## Example jobs

| Sample | Mode | Notes |
|---|---|---|
| `config/examples/example-job-local.yaml` | docker | runnable locally, no cloud |
| `config/examples/example-job-aws-k8s.yaml` | k8s | runnable end-to-end on AWS (alpine smoke test, spot) |
| `config/examples/example-job-cron.yaml` | k8s | cron-triggered smoke test (alpine, spot) |

## Configuration

See [docs/config-reference.md](docs/config-reference.md) for the full schema
and [config/examples/](config/examples/) for working samples.

## Development

```bash
cd orchestrator
go vet ./...
go test ./...        # docker integration tests skip without a daemon
go build ./cmd/openbench
```


