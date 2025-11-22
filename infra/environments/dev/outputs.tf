output "s3_raw_bucket" {
  description = "Bucket for raw video uploads"
  value       = aws_s3_bucket.raw_ingest.bucket
}

output "s3_processed_bucket" {
  description = "Bucket for public HLS streaming"
  value       = aws_s3_bucket.processed.bucket
}

output "sqs_queue_url" {
  description = "URL of the SQS queue"
  value       = aws_sqs_queue.video_queue.id
}

output "worker_access_key" {
  description = "AWS Access Key for the Worker App"
  value       = aws_iam_access_key.worker_key.id
  sensitive   = true
}

output "worker_secret_key" {
  description = "AWS Secret Key for the Worker App"
  value       = aws_iam_access_key.worker_key.secret
  sensitive   = true
}
