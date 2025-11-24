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

output "CDN_DOMAIN" {
  description = "CloudFront Domain Name"
  value       = aws_cloudfront_distribution.s3_distribution.domain_name
}

output "API_ENDPOINT" {
  description = "Public Load Balancer DNS"
  value       = "http://${aws_lb.main.dns_name}"
}

output "ECR_API_URL" {
  description = "URL for API Docker Image"
  value       = aws_ecr_repository.api.repository_url
}

output "ECR_WORKER_URL" {
  description = "URL for Worker Docker Image"
  value       = aws_ecr_repository.worker.repository_url
}
