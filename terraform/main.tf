terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }

  # Remote state with a per-run key is passed entirely via -backend-config at
  # init time (bucket/region/dynamodb_table/key), so nothing cloud-specific or
  # run-specific lives in the committed config.
  backend "s3" {
  }
}

provider "aws" {
  region = var.region
}

module "test_nodes" {
  source        = "./modules/aws-test-nodes"
  nodes         = var.nodes
  instance_type = var.instance_type
  region        = var.region
  run_id        = var.run_id
  ttl_minutes   = var.ttl_minutes
  enable_k3s    = var.enable_k3s
  spot          = var.spot
  state_bucket  = var.state_bucket
  state_region  = var.state_region
  tags          = var.tags
}

output "node_ids" {
  value = module.test_nodes.node_ids
}

output "node_public_ips" {
  value = module.test_nodes.node_public_ips
}

output "k3s_server_url" {
  value = var.enable_k3s ? module.test_nodes.k3s_server_url : ""
}
