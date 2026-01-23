output "notification_id" {
  description = "ID of the S3 bucket notification configuration"
  value       = aws_s3_bucket_notification.bucket_notification.id
}

output "bucket_name" {
  description = "Name of the bucket with notifications configured"
  value       = var.bucket_name
}

output "queue_arn" {
  description = "ARN of the queue receiving notifications"
  value       = var.queue_arn
}

