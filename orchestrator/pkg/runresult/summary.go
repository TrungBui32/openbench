// Package runresult defines the structured output every run produces.
// This is the artifact Jenkins parses for pass/fail metrics.
package runresult

import (
	"crypto/rand"
	"fmt"
	"time"
)

const (
	StatusPass = "pass"
	StatusFail = "fail"
)

type NodeResult struct {
	NodeID   int    `json:"node_id"`
	ExitCode int    `json:"exit_code"`
	Status   string `json:"status"`
	LogPath  string `json:"log_path"`
}

type InfraInfo struct {
	Provider     string `json:"provider"`
	InstanceType string `json:"instance_type"`
	Region       string `json:"region"`
}

type Summary struct {
	RunID           string       `json:"run_id"`
	JobName         string       `json:"job_name"`
	Status          string       `json:"status"`
	OnFailurePolicy string       `json:"on_failure_policy"`
	StartedAt       time.Time    `json:"started_at"`
	FinishedAt      time.Time    `json:"finished_at"`
	Nodes           []NodeResult `json:"nodes"`
	PassCount       int          `json:"pass_count"`
	FailCount       int          `json:"fail_count"`
	Infra           InfraInfo    `json:"infra"`
}

// NewRunID derives a run identifier from the job name and wall clock, plus a
// random suffix so two runs of the same job within the same second never
// collide (container/job names and terraform state keys all derive from it).
func NewRunID(jobName string) string {
	return fmt.Sprintf("%s-%s-%06x", jobName, time.Now().Format("20060102-150405"), randomSuffix())
}

// randomSuffix returns a 24-bit random value as 6 hex chars.
func randomSuffix() int {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand is effectively never unavailable, but fall back to
		// clock-based entropy so run ids cannot silently collide.
		return int(time.Now().UnixNano() & 0xffffff)
	}
	return int(b[0])<<16 | int(b[1])<<8 | int(b[2])
}
