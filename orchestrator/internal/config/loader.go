package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// Load reads job.yaml and returns the typed job plus the raw document.
// The raw document is needed for JSON Schema validation, which operates on
// the untyped YAML tree rather than the typed struct.
func Load(path string) (*Job, any, error) {
	raw, err := readRaw(path)
	if err != nil {
		return nil, nil, err
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return nil, nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Job.Name == "" {
		return nil, nil, fmt.Errorf("config has no job section")
	}
	return &cfg.Job, raw, nil
}

func readRaw(path string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var raw any
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return raw, nil
}
