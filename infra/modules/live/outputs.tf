output "service_name" {
  description = "Name of the ECS service"
  value       = aws_ecs_service.live.name
}

output "task_definition_arn" {
  description = "ARN of the task definition"
  value       = aws_ecs_task_definition.live.arn
}

output "security_group_id" {
  description = "Security group ID"
  value       = aws_security_group.live.id
}

output "srt_url" {
  description = "SRT ingest URL"
  value       = var.enable_nlb ? "srt://${aws_lb.live[0].dns_name}:9998" : null
}

output "rtmp_url" {
  description = "RTMP ingest URL"
  value       = var.enable_nlb ? "rtmp://${aws_lb.live[0].dns_name}:1935/live" : null
}

output "api_url" {
  description = "API URL for live management"
  value       = null # Placeholder - would be set if API endpoint is configured
}

output "nlb_dns_name" {
  description = "NLB DNS name"
  value       = var.enable_nlb ? aws_lb.live[0].dns_name : null
}

output "nlb_arn" {
  description = "NLB ARN"
  value       = var.enable_nlb ? aws_lb.live[0].arn : null
}
