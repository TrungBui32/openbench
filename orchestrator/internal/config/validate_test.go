package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

const validLocalYAML = `
job:
  name: hello-local
  image: alpine:3.20
  command: "echo hello"
  nodes: 3
  timeout_minutes: 10
  on_failure: fail-fast
  infra:
    provider: aws
    instance_type: t3.small
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: local
    dir: /var/openbench/logs
    path_prefix: hello-local/{run_id}/
  trigger:
    type: manual
    cron_schedule: null
`

func load(t *testing.T, yamlStr string) (*Job, any) {
	t.Helper()
	var cfg Config
	if err := yaml.Unmarshal([]byte(yamlStr), &cfg); err != nil {
		t.Fatalf("parse typed: %v", err)
	}
	var raw any
	if err := yaml.Unmarshal([]byte(yamlStr), &raw); err != nil {
		t.Fatalf("parse raw: %v", err)
	}
	return &cfg.Job, raw
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{"valid local", validLocalYAML, false},
		{"valid s3", `
job:
  name: gpu-driver-regression
  image: myrepo/gpu-test:latest
  command: "./run_test.sh"
  nodes: 5
  timeout_minutes: 45
  infra:
    provider: aws
    instance_type: g4dn.xlarge
    region: us-east-1
    ttl_minutes: 60
  storage:
    type: s3
    bucket: my-test-logs
    path_prefix: gpu-driver/{run_id}/
  trigger:
    type: manual
    cron_schedule: null
`, false},
		{"valid cron", `
job:
  name: nightly
  image: img:1
  command: "x"
  nodes: 1
  timeout_minutes: 10
  infra:
    provider: aws
    instance_type: t3.small
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: local
    dir: /tmp/logs
    path_prefix: nightly/{run_id}/
  trigger:
    type: cron
    cron_schedule: "0 2 * * *"
`, false},
		{"local missing dir", `
job:
  name: x
  image: img:1
  command: "x"
  nodes: 1
  timeout_minutes: 10
  infra:
    provider: aws
    instance_type: t3.small
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: local
    path_prefix: x/{run_id}/
  trigger:
    type: manual
    cron_schedule: null
`, true},
		{"s3 missing bucket", `
job:
  name: x
  image: img:1
  command: "x"
  nodes: 1
  timeout_minutes: 10
  infra:
    provider: aws
    instance_type: t3.small
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: s3
    path_prefix: x/{run_id}/
  trigger:
    type: manual
    cron_schedule: null
`, true},
		{"cron missing schedule", `
job:
  name: x
  image: img:1
  command: "x"
  nodes: 1
  timeout_minutes: 10
  infra:
    provider: aws
    instance_type: t3.small
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: local
    dir: /tmp/logs
    path_prefix: x/{run_id}/
  trigger:
    type: cron
    cron_schedule: null
`, true},
		{"cron invalid schedule", `
job:
  name: x
  image: img:1
  command: "x"
  nodes: 1
  timeout_minutes: 10
  infra:
    provider: aws
    instance_type: t3.small
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: local
    dir: /tmp/logs
    path_prefix: x/{run_id}/
  trigger:
    type: cron
    cron_schedule: "not a cron"
`, true},
		{"nodes zero", `
job:
  name: x
  image: img:1
  command: "x"
  nodes: 0
  timeout_minutes: 10
  infra:
    provider: aws
    instance_type: t3.small
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: local
    dir: /tmp/logs
    path_prefix: x/{run_id}/
  trigger:
    type: manual
    cron_schedule: null
`, true},
		{"timeout exceeds ttl", `
job:
  name: x
  image: img:1
  command: "x"
  nodes: 1
  timeout_minutes: 60
  infra:
    provider: aws
    instance_type: t3.small
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: local
    dir: /tmp/logs
    path_prefix: x/{run_id}/
  trigger:
    type: manual
    cron_schedule: null
`, true},
		{"unsupported instance type", `
job:
  name: x
  image: img:1
  command: "x"
  nodes: 1
  timeout_minutes: 10
  infra:
    provider: aws
    instance_type: g5.48xlarge
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: local
    dir: /tmp/logs
    path_prefix: x/{run_id}/
  trigger:
    type: manual
    cron_schedule: null
`, true},
		{"absolute path prefix", `
job:
  name: x
  image: img:1
  command: "x"
  nodes: 1
  timeout_minutes: 10
  infra:
    provider: aws
    instance_type: t3.small
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: local
    dir: /tmp/logs
    path_prefix: /etc/passwd
  trigger:
    type: manual
    cron_schedule: null
`, true},
		{"path traversal", `
job:
  name: x
  image: img:1
  command: "x"
  nodes: 1
  timeout_minutes: 10
  infra:
    provider: aws
    instance_type: t3.small
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: local
    dir: /tmp/logs
    path_prefix: ../../etc/{run_id}/
  trigger:
    type: manual
    cron_schedule: null
`, true},
		{"unknown provider", `
job:
  name: x
  image: img:1
  command: "x"
  nodes: 1
  timeout_minutes: 10
  infra:
    provider: gcp
    instance_type: t3.small
    region: us-east-1
    ttl_minutes: 15
  storage:
    type: local
    dir: /tmp/logs
    path_prefix: x/{run_id}/
  trigger:
    type: manual
    cron_schedule: null
`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job, raw := load(t, tc.yaml)
			err := Validate(job, raw)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestOnFailureDefaultsToFailFast(t *testing.T) {
	job, raw := load(t, validLocalYAML)
	job.OnFailure = ""
	if err := Validate(job, raw); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.OnFailure != "fail-fast" {
		t.Fatalf("expected on_failure to default to fail-fast, got %q", job.OnFailure)
	}
}
