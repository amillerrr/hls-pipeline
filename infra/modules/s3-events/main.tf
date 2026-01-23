terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}

# Variables
variable "bucket_name" {
  description = "Name of the S3 bucket to configure notifications for"
  type        = string
}

variable "bucket_arn" {
  description = "ARN of the S3 bucket"
  type        = string
}

variable "queue_arn" {
  description = "ARN of the SQS queue to receive notifications"
  type        = string
}

variable "queue_url" {
  description = "URL of the SQS queue"
  type        = string
}

variable "filter_prefix" {
  description = "S3 key prefix filter (e.g., 'uploads/')"
  type        = string
  default     = ""
}

variable "filter_suffix" {
  description = "S3 key suffix filter (e.g., '.mp4')"
  type        = string
  default     = ""
}

variable "events" {
  description = "List of S3 events to trigger notifications"
  type        = list(string)
  default     = ["s3:ObjectCreated:*"]
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

# SQS Queue Policy to allow S3 to send messages
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
          ArnLike = {
            "aws:SourceArn" = var.bucket_arn
          }
        }
      }
    ]
  })
}

# S3 Bucket Notification Configuration
resource "aws_s3_bucket_notification" "upload_notification" {
  bucket = var.bucket_name

  queue {
    queue_arn = var.queue_arn
    events    = var.events

    dynamic "filter_prefix" {
      for_each = var.filter_prefix != "" ? [var.filter_prefix] : []
      content {
        filter_prefix = filter_prefix.value
      }
    }

    dynamic "filter_suffix" {
      for_each = var.filter_suffix != "" ? [var.filter_suffix] : []
      content {
        filter_suffix = filter_suffix.value
      }
    }
  }

  depends_on = [aws_sqs_queue_policy.s3_notification]
}

# Outputs
output "notification_id" {
  description = "ID of the S3 bucket notification configuration"
  value       = aws_s3_bucket_notification.upload_notification.id
}

