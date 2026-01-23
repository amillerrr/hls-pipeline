# S3 Buckets
output "raw_bucket_name" {
  description = "Name of the raw uploads bucket"
  value       = aws_s3_bucket.raw.id
}

output "raw_bucket_arn" {
  description = "ARN of the raw uploads bucket"
  value       = aws_s3_bucket.raw.arn
}

output "processed_bucket_name" {
  description = "Name of the processed outputs bucket"
  value       = aws_s3_bucket.processed.id
}

output "processed_bucket_arn" {
  description = "ARN of the processed outputs bucket"
  value       = aws_s3_bucket.processed.arn
}

# SQS Queues
output "jobs_queue_url" {
  description = "URL of the SQS jobs queue"
  value       = aws_sqs_queue.jobs.url
}

output "jobs_queue_arn" {
  description = "ARN of the SQS jobs queue"
  value       = aws_sqs_queue.jobs.arn
}

output "dlq_url" {
  description = "URL of the dead letter queue"
  value       = aws_sqs_queue.dlq.url
}

output "dlq_arn" {
  description = "ARN of the dead letter queue"
  value       = aws_sqs_queue.dlq.arn
}

# DynamoDB
output "dynamodb_table_name" {
  description = "DynamoDB table name"
  value       = aws_dynamodb_table.videos.name
}

output "dynamodb_table_arn" {
  description = "DynamoDB table ARN"
  value       = aws_dynamodb_table.videos.arn
}

# ECS
output "ecs_cluster_name" {
  description = "ECS cluster name"
  value       = aws_ecs_cluster.main.name
}

output "ecs_cluster_arn" {
  description = "ECS cluster ARN"
  value       = aws_ecs_cluster.main.arn
}

# CloudFront
output "cdn_domain" {
  description = "CloudFront distribution domain"
  value       = aws_cloudfront_distribution.main.domain_name
}

output "cdn_distribution_id" {
  description = "CloudFront distribution ID"
  value       = aws_cloudfront_distribution.main.id
}

output "cdn_arn" {
  description = "CloudFront distribution ARN"
  value       = aws_cloudfront_distribution.main.arn
}

# IAM Roles
output "api_task_role_arn" {
  description = "ARN of the API task IAM role"
  value       = aws_iam_role.api_task.arn
}

output "worker_task_role_arn" {
  description = "ARN of the Worker task IAM role"
  value       = aws_iam_role.worker_task.arn
}

output "task_execution_role_arn" {
  description = "ARN of the ECS task execution role"
  value       = aws_iam_role.task_execution.arn
}

# ECR Repositories
output "ecr_api_repository_url" {
  description = "URL for API Docker image repository"
  value       = aws_ecr_repository.api.repository_url
}

output "ecr_worker_repository_url" {
  description = "URL for Worker Docker image repository"
  value       = aws_ecr_repository.worker.repository_url
}

# URL Templates
output "upload_url_template" {
  description = "Template for upload URLs"
  value       = "s3://${aws_s3_bucket.raw.id}/uploads/{videoId}/{filename}"
}

output "playback_url_template" {
  description = "Template for playback URLs"
  value       = "https://${aws_cloudfront_distribution.main.domain_name}/processed/{videoId}/master.m3u8"
}

# Live Streaming (conditional)
output "live_srt_url" {
  description = "SRT ingest URL for live streaming"
  value       = var.enable_live && length(module.live) > 0 ? module.live[0].srt_url : null
}

output "live_rtmp_url" {
  description = "RTMP ingest URL for live streaming"
  value       = var.enable_live && length(module.live) > 0 ? module.live[0].rtmp_url : null
}

output "live_api_url" {
  description = "API URL for live streaming management"
  value       = var.enable_live && length(module.live) > 0 ? module.live[0].api_url : null
}

# Environment Variables for Services
output "api_environment_variables" {
  description = "Environment variables for API service"
  value = {
    AWS_REGION        = var.aws_region
    S3_BUCKET         = aws_s3_bucket.raw.id
    PROCESSED_BUCKET  = aws_s3_bucket.processed.id
    SQS_QUEUE_URL     = aws_sqs_queue.jobs.url
    DYNAMODB_TABLE    = aws_dynamodb_table.videos.name
    CDN_DOMAIN        = aws_cloudfront_distribution.main.domain_name
    ENVIRONMENT       = var.environment
  }
  sensitive = false
}

output "worker_environment_variables" {
  description = "Environment variables for Worker service"
  value = {
    AWS_REGION        = var.aws_region
    S3_BUCKET         = aws_s3_bucket.raw.id
    PROCESSED_BUCKET  = aws_s3_bucket.processed.id
    SQS_QUEUE_URL     = aws_sqs_queue.jobs.url
    DYNAMODB_TABLE    = aws_dynamodb_table.videos.name
    CDN_DOMAIN        = aws_cloudfront_distribution.main.domain_name
    ENVIRONMENT       = var.environment
  }
  sensitive = false
}
