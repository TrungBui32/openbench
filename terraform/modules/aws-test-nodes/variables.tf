variable "nodes" {
  description = "Number of EC2 instances."
  type        = number
}

variable "instance_type" {
  type = string
}

variable "region" {
  type = string
}

variable "run_id" {
  type = string
}

variable "ttl_minutes" {
  type = number
}

variable "enable_k3s" {
  type    = bool
  default = false
}

variable "spot" {
  description = "Launch instances as EC2 spot (interruption-capable, lower cost)."
  type        = bool
  default     = false
}

variable "k3s_api_cidr" {
  description = "CIDR allowed to reach the k3s API server (6443) from outside the security group."
  type        = string
  default     = "0.0.0.0/0"
}

variable "state_bucket" {
  description = "S3 bucket the node stages its kubeconfig into (same as the remote state bucket)."
  type        = string
  default     = ""
}

variable "state_region" {
  type    = string
  default = ""
}

variable "tags" {
  type    = map(string)
  default = {}
}

variable "ami_owner" {
  description = "Canonical Ubuntu account."
  type        = string
  default     = "099720109477"
}

variable "ami_name" {
  type    = string
  default = "ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"
}
