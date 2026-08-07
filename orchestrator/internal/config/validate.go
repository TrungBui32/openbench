package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/robfig/cron/v3"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed schema.json
var schemaJSON []byte

var (
	// SupportedInstanceTypes is the allowlist of EC2 instance types a job may
	// request, bounding cost exposure from an arbitrary infra.instance_type.
	SupportedInstanceTypes = map[string]bool{
		"t3.small": true, "t3.medium": true, "t3.large": true, "t3.xlarge": true,
		"t2.small": true, "t2.medium": true,
		"m5.large": true, "m5.xlarge": true,
		"c5.large": true, "c5.xlarge": true,
		"g4dn.xlarge": true, "g4dn.2xlarge": true,
	}

	// pathPrefixRe mirrors the JSON Schema pattern for storage.path_prefix.
	// Braces are allowed for the {run_id} placeholder.
	pathPrefixRe = regexp.MustCompile(`^[a-zA-Z0-9/_\-{}]+$`)
)

// Validate runs JSON Schema validation against the raw document, then
// semantic checks against the typed struct. On failure the job is untouched.
func Validate(job *Job, raw any) error {
	if err := validateSchema(raw); err != nil {
		return fmt.Errorf("schema validation: %w", err)
	}
	return validateSemantic(job)
}

func validateSchema(raw any) error {
	c := jsonschema.NewCompiler()
	if err := c.AddResource("job.schema.json", bytes.NewReader(schemaJSON)); err != nil {
		return err
	}
	sch, err := c.Compile("job.schema.json")
	if err != nil {
		return err
	}
	return sch.Validate(raw)
}

func validateSemantic(job *Job) error {
	if job.OnFailure == "" {
		job.OnFailure = "fail-fast"
	}
	if job.OnFailure != "fail-fast" && job.OnFailure != "collect-all" {
		return errors.New("on_failure must be one of fail-fast or collect-all")
	}
	if job.TimeoutMinutes <= 0 {
		return errors.New("timeout_minutes must be a positive integer")
	}
	if job.Infra.TTLMinutes > 0 && job.TimeoutMinutes > job.Infra.TTLMinutes {
		return fmt.Errorf("timeout_minutes (%d) must be <= infra.ttl_minutes (%d)", job.TimeoutMinutes, job.Infra.TTLMinutes)
	}
	if job.Infra.Provider != "" && job.Infra.Provider != "aws" {
		return fmt.Errorf("infra.provider %q is not supported (only aws)", job.Infra.Provider)
	}
	if job.Infra.InstanceType != "" && !SupportedInstanceTypes[job.Infra.InstanceType] {
		return fmt.Errorf("infra.instance_type %q is not in the supported instance-type map", job.Infra.InstanceType)
	}
	if err := validatePathPrefix(job.Storage.PathPrefix); err != nil {
		return err
	}
	if job.Trigger.Type == "cron" {
		if _, err := cron.ParseStandard(job.Trigger.CronSchedule); err != nil {
			return fmt.Errorf("trigger.cron_schedule %q is not a valid cron expression: %w", job.Trigger.CronSchedule, err)
		}
	}
	return nil
}

func validatePathPrefix(prefix string) error {
	if strings.HasPrefix(prefix, "/") {
		return errors.New("storage.path_prefix must not be an absolute path")
	}
	if strings.Contains(prefix, "..") {
		return errors.New("storage.path_prefix must not contain '..'")
	}
	if !pathPrefixRe.MatchString(prefix) {
		return errors.New("storage.path_prefix contains disallowed characters")
	}
	return nil
}
