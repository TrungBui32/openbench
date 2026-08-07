variable "nodes" {
  description = "Number of EC2 instances to provision (parallel replicas)."
  type        = number
}

variable "instance_type" {
  description = "EC2 instance type (must be in the orchestrator's supported map)."
  type        = string
}

variable "region" {
  description = "AWS region."
  type        = string
}

variable "run_id" {
  description = "Unique run identifier; used for state key isolation and tags."
  type        = string
}

variable "ttl_minutes" {
  description = "TTL after which the TTL watcher force-terminates these instances."
  type        = number
}

variable "enable_k3s" {
  description = "Bootstrap k3s on node 0 and join the rest (cheaper alternative to EKS)."
  type        = bool
  default     = false
}

variable "spot" {
  description = "Launch nodes as EC2 spot instances (interruption-capable, lower cost)."
  type        = bool
  default     = false
}

variable "state_bucket" {
  description = "S3 bucket used for remote state; also the staging location for the k3s kubeconfig handoff."
  type        = string
  default     = ""
}

variable "state_region" {
  description = "Region of the remote state bucket."
  type        = string
  default     = ""
}

variable "tags" {
  description = "Additional tags applied to all resources."
  type        = map(string)
  default     = {}
}
