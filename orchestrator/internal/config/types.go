package config

type Config struct {
	Job Job `mapstructure:"job" yaml:"job"`
}

type Job struct {
	Name           string   `mapstructure:"name" yaml:"name" json:"name"`
	Image          string   `mapstructure:"image" yaml:"image" json:"image"`
	Command        string   `mapstructure:"command" yaml:"command" json:"command"`
	Nodes          int      `mapstructure:"nodes" yaml:"nodes" json:"nodes"`
	TimeoutMinutes int      `mapstructure:"timeout_minutes" yaml:"timeout_minutes" json:"timeout_minutes"`
	OnFailure      string   `mapstructure:"on_failure" yaml:"on_failure" json:"on_failure"`
	Env            []EnvVar `mapstructure:"env" yaml:"env" json:"env"`
	Secrets        []Secret `mapstructure:"secrets" yaml:"secrets" json:"secrets"`
	Infra          Infra    `mapstructure:"infra" yaml:"infra" json:"infra"`
	Storage        Storage  `mapstructure:"storage" yaml:"storage" json:"storage"`
	Trigger        Trigger  `mapstructure:"trigger" yaml:"trigger" json:"trigger"`
}

type EnvVar struct {
	Name  string `mapstructure:"name" yaml:"name" json:"name"`
	Value string `mapstructure:"value" yaml:"value" json:"value"`
}

type Secret struct {
	Name   string `mapstructure:"name" yaml:"name" json:"name"`
	Source string `mapstructure:"source" yaml:"source" json:"source"`
	Key    string `mapstructure:"key" yaml:"key" json:"key"`
}

type Infra struct {
	Provider     string `mapstructure:"provider" yaml:"provider" json:"provider"`
	InstanceType string `mapstructure:"instance_type" yaml:"instance_type" json:"instance_type"`
	Region       string `mapstructure:"region" yaml:"region" json:"region"`
	TTLMinutes   int    `mapstructure:"ttl_minutes" yaml:"ttl_minutes" json:"ttl_minutes"`
	Spot         bool   `mapstructure:"spot" yaml:"spot" json:"spot"`
}

type Storage struct {
	Type       string `mapstructure:"type" yaml:"type" json:"type"`
	Dir        string `mapstructure:"dir" yaml:"dir" json:"dir"`
	Bucket     string `mapstructure:"bucket" yaml:"bucket" json:"bucket"`
	PathPrefix string `mapstructure:"path_prefix" yaml:"path_prefix" json:"path_prefix"`
}

type Trigger struct {
	Type         string `mapstructure:"type" yaml:"type" json:"type"`
	CronSchedule string `mapstructure:"cron_schedule" yaml:"cron_schedule" json:"cron_schedule"`
}
