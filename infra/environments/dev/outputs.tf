output "S3_BUCKET" {
  description = "Bucket for raw video uploads"
  value       = aws_s3_bucket.raw_ingest.bucket
}

output "PROCESSED_BUCKET" {
  description = "Bucket for public HLS streaming"
  value       = aws_s3_bucket.processed.bucket
}

output "SQS_QUEUE_URL" {
  description = "URL of the SQS queue"
  value       = aws_sqs_queue.video_queue.id
}

output "AWS_ACCESS_KEY_ID" {
  description = "AWS Access Key for the Worker App"
  value       = aws_iam_access_key.worker_key.id
  sensitive   = true
}

output "AWS_SECRET_ACCESS_KEY" {
  description = "AWS Secret Key for the Worker App"
  value       = aws_iam_access_key.worker_key.secret
  sensitive   = true
}

output "CDN_DOMAIN" {
  description = "CloudFront Domain Name"
  value       = aws_cloudfront_distribution.s3_distribution.domain_name
}
