output "node_ids" {
  value = aws_instance.test_nodes[*].id
}

output "node_public_ips" {
  value = aws_instance.test_nodes[*].public_ip
}

output "k3s_server_url" {
  value = var.enable_k3s ? "https://${aws_instance.test_nodes[0].public_ip}:6443" : ""
}
