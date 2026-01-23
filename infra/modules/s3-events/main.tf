# S3 Events Module - Main Configuration
# This module sets up S3 event notifications to trigger SQS messages
# when new objects are uploaded to the specified bucket.

# SQS Queue Policy - Allow S3 to send messages
resource "aws_sqs_queue_policy" "s3_notification" {
  queue_url = var.queue_url

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AllowS3Notification"
        Effect = "Allow"
        Principal = {
          Service = "s3.amazonaws.com"
        }
        Action   = "sqs:SendMessage"
        Resource = var.queue_arn
        Condition = {
          ArnEquals = {
            "aws:SourceArn" = var.bucket_arn
          }
        }
      }
    ]
  })
}

# S3 Bucket Notification
resource "aws_s3_bucket_notification" "bucket_notification" {
  bucket = var.bucket_name

  queue {
    queue_arn     = var.queue_arn
    events        = var.events
    filter_prefix = var.filter_prefix
    filter_suffix = var.filter_suffix != "" ? var.filter_suffix : null
  }

  depends_on = [aws_sqs_queue_policy.s3_notification]
}
