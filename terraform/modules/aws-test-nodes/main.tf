data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = [var.ami_owner]

  filter {
    name   = "name"
    values = [var.ami_name]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

data "aws_vpc" "default" {
  default = true
}

resource "aws_security_group" "test_nodes" {
  count       = var.nodes > 0 ? 1 : 0
  name_prefix = "openbench-${var.run_id}-"
  vpc_id      = data.aws_vpc.default.id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Cluster edge endpoint: the orchestrator reaches the k3s API server via the
  # node's public IP after provisioning. Auth is enforced by the kubeconfig
  # client cert the node hands back through S3 (see user_data.sh).
  ingress {
    from_port   = 6443
    to_port     = 6443
    protocol    = "tcp"
    cidr_blocks = [var.k3s_api_cidr]
  }

  # Inter-node traffic for a real multi-node cluster: k3s agents register on the
  # server's 6443, the server reaches agents' kubelet on 10250, and Flannel
  # carries pod traffic over VXLAN (8472/udp). All nodes share this SG, so a
  # self-referencing rule covers server <-> agent.
  ingress {
    from_port = 6443
    to_port   = 6443
    protocol  = "tcp"
    self      = true
  }
  ingress {
    from_port = 10250
    to_port   = 10250
    protocol  = "tcp"
    self      = true
  }
  ingress {
    from_port = 8472
    to_port   = 8472
    protocol  = "udp"
    self      = true
  }

  tags = merge(var.tags, {
    Name               = "openbench-${var.run_id}-sg"
    "openbench:run_id" = var.run_id
  })
}

# IAM role so nodes can stage/recover run artifacts (kubeconfig for the server,
# server address for the joining agents) in the remote state bucket. Only needed
# when there is an S3 handoff, so it's gated on enable_k3s && state_bucket.
resource "aws_iam_role" "test_nodes" {
  count = var.enable_k3s && var.state_bucket != "" ? 1 : 0
  name  = "openbench-${var.run_id}-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy" "test_nodes" {
  count = var.enable_k3s && var.state_bucket != "" ? 1 : 0
  name  = "openbench-${var.run_id}-handoff"
  role  = aws_iam_role.test_nodes[0].id

  # Scoped to this run's handoff objects only, so a compromised node can read
  # or write nothing outside its own cluster bootstrap.
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:PutObject", "s3:GetObject"]
      Resource = "arn:aws:s3:::${var.state_bucket}/kubeconfig/${var.run_id}/*"
    }]
  })
}

resource "aws_iam_instance_profile" "test_nodes" {
  count = var.enable_k3s && var.state_bucket != "" ? 1 : 0
  name  = "openbench-${var.run_id}-profile"
  role  = aws_iam_role.test_nodes[count.index].name
}

resource "aws_instance" "test_nodes" {
  count                  = var.nodes
  ami                    = data.aws_ami.ubuntu.id
  instance_type          = var.instance_type
  vpc_security_group_ids = [aws_security_group.test_nodes[0].id]
  iam_instance_profile   = try(aws_iam_instance_profile.test_nodes[0].name, null)

  dynamic "instance_market_options" {
    for_each = var.spot ? [1] : []
    content {
      market_type = "spot"
    }
  }

  user_data = templatefile("${path.module}/user_data.sh", {
    run_id       = var.run_id
    enable_k3s   = var.enable_k3s
    node_index   = count.index
    nodes        = var.nodes
    state_bucket = var.state_bucket
    state_region = var.state_region
  })

  tags = merge(var.tags, {
    Name                       = "openbench-${var.run_id}-${count.index}"
    "openbench:run_id"         = var.run_id
    "openbench:ttl_expires_at" = timeadd(timestamp(), "${var.ttl_minutes}m")
  })
}
