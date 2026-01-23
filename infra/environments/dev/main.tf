terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }

  backend "s3" {
    # Configure your backend
    # bucket         = "your-terraform-state-bucket"
    # key            = "hls-pipeline/terraform.tfstate"
    # region         = "us-west-2"
    # dynamodb_table = "terraform-locks"
    # encrypt        = true
  }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = "hls-pipeline"
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}

# Local variables
locals {
  name_prefix = "hls-pipeline-${var.environment}"
}

# S3 Buckets
# Raw uploads bucket
resource "aws_s3_bucket" "raw" {
  bucket = "${local.name_prefix}-raw"

  tags = {
    Name = "${local.name_prefix}-raw"
  }
}

resource "aws_s3_bucket_versioning" "raw" {
  bucket = aws_s3_bucket.raw.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "raw" {
  bucket = aws_s3_bucket.raw.id

  rule {
    id     = "cleanup-old-uploads"
    status = "Enabled"

    filter {
      prefix = "uploads/"
    }

    expiration {
      days = var.raw_bucket_expiration_days
    }

    noncurrent_version_expiration {
      noncurrent_days = 1
    }
  }
}

# Processed outputs bucket
resource "aws_s3_bucket" "processed" {
  bucket = "${local.name_prefix}-processed"

  tags = {
    Name = "${local.name_prefix}-processed"
  }
}

resource "aws_s3_bucket_cors_configuration" "processed" {
  bucket = aws_s3_bucket.processed.id

  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET", "HEAD"]
    allowed_origins = var.cors_allowed_origins
    expose_headers  = ["ETag", "Content-Length", "Content-Type"]
    max_age_seconds = 3600
  }
}


# SQS Queue for Job Processing
# Dead Letter Queue
resource "aws_sqs_queue" "dlq" {
  name                      = "${local.name_prefix}-dlq"
  message_retention_seconds = 1209600 # 14 days

  tags = {
    Name = "${local.name_prefix}-dlq"
  }
}

# Main processing queue
resource "aws_sqs_queue" "jobs" {
  name                       = "${local.name_prefix}-jobs"
  visibility_timeout_seconds = var.sqs_visibility_timeout_seconds
  message_retention_seconds  = var.sqs_message_retention_days * 86400
  receive_wait_time_seconds  = var.sqs_receive_wait_time_seconds

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
    maxReceiveCount     = var.sqs_max_receive_count
  })

  tags = {
    Name = "${local.name_prefix}-jobs"
  }
}

# S3 Event Notifications -> SQS (Event-Driven Architecture)
module "s3_events" {
  source = "./modules/s3-events"

  bucket_name   = aws_s3_bucket.raw.id
  bucket_arn    = aws_s3_bucket.raw.arn
  queue_arn     = aws_sqs_queue.jobs.arn
  queue_url     = aws_sqs_queue.jobs.url
  
  # Filter to only process video uploads
  filter_prefix = "uploads/"
  
  # Optionally filter by file extension (empty = all files)
  # filter_suffix = ".mp4"
  
  events = ["s3:ObjectCreated:*"]

  tags = {
    Name = "${local.name_prefix}-s3-events"
  }
}

# DynamoDB Table
resource "aws_dynamodb_table" "videos" {
  name         = "${local.name_prefix}-videos"
  billing_mode = var.dynamodb_billing_mode
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  attribute {
    name = "userId"
    type = "S"
  }

  attribute {
    name = "status"
    type = "S"
  }

  global_secondary_index {
    name            = "userId-index"
    hash_key        = "userId"
    projection_type = "ALL"
  }

  global_secondary_index {
    name            = "status-index"
    hash_key        = "status"
    projection_type = "ALL"
  }

  tags = {
    Name = "${local.name_prefix}-videos"
  }
}

# ECS Cluster
resource "aws_ecs_cluster" "main" {
  name = local.name_prefix

  setting {
    name  = "containerInsights"
    value = "enabled"
  }

  tags = {
    Name = local.name_prefix
  }
}

resource "aws_ecs_cluster_capacity_providers" "main" {
  cluster_name = aws_ecs_cluster.main.name

  capacity_providers = ["FARGATE", "FARGATE_SPOT"]

  default_capacity_provider_strategy {
    capacity_provider = "FARGATE_SPOT"
    weight            = 1
  }
}

# CloudFront Distribution
resource "aws_cloudfront_origin_access_control" "processed" {
  name                              = "${local.name_prefix}-processed-oac"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

resource "aws_cloudfront_distribution" "main" {
  enabled             = true
  is_ipv6_enabled     = true
  comment             = "HLS Pipeline CDN - ${var.environment}"
  default_root_object = ""
  price_class         = var.cdn_price_class

  origin {
    domain_name              = aws_s3_bucket.processed.bucket_regional_domain_name
    origin_id                = "processed-bucket"
    origin_access_control_id = aws_cloudfront_origin_access_control.processed.id
  }

  default_cache_behavior {
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "processed-bucket"

    forwarded_values {
      query_string = false
      headers      = ["Origin", "Access-Control-Request-Headers", "Access-Control-Request-Method"]

      cookies {
        forward = "none"
      }
    }

    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = 0
    default_ttl            = 86400
    max_ttl                = 31536000
    compress               = true
  }

  # Cache behavior for HLS manifests (short TTL for live)
  ordered_cache_behavior {
    path_pattern     = "*.m3u8"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "processed-bucket"

    forwarded_values {
      query_string = true
      cookies {
        forward = "none"
      }
    }

    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = 0
    default_ttl            = 2     # 2 seconds for live manifests
    max_ttl                = 10
    compress               = true
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = true
  }

  tags = {
    Name = "${local.name_prefix}-cdn"
  }
}

# S3 bucket policy for CloudFront access
resource "aws_s3_bucket_policy" "processed" {
  bucket = aws_s3_bucket.processed.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AllowCloudFrontAccess"
        Effect = "Allow"
        Principal = {
          Service = "cloudfront.amazonaws.com"
        }
        Action   = "s3:GetObject"
        Resource = "${aws_s3_bucket.processed.arn}/*"
        Condition = {
          StringEquals = {
            "AWS:SourceArn" = aws_cloudfront_distribution.main.arn
          }
        }
      }
    ]
  })
}

# Live Streaming Infrastructure (Optional)
module "live" {
  source = "./modules/live"
  count  = var.enable_live ? 1 : 0

  environment       = var.environment
  vpc_id            = var.vpc_id
  subnet_ids        = var.subnet_ids
  cluster_id        = aws_ecs_cluster.main.id
  cluster_name      = aws_ecs_cluster.main.name
  live_image        = var.live_image
  output_bucket     = aws_s3_bucket.processed.id
  output_bucket_arn = aws_s3_bucket.processed.arn
  cdn_domain        = aws_cloudfront_distribution.main.domain_name

  cpu           = var.live_cpu
  memory        = var.live_memory
  desired_count = var.live_desired_count
  enable_nlb    = var.enable_live_nlb

  tags = {
    Name = "${local.name_prefix}-live"
  }
}

# Outputs
output "raw_bucket_name" {
  description = "Name of the raw uploads bucket"
  value       = aws_s3_bucket.raw.id
}

output "processed_bucket_name" {
  description = "Name of the processed outputs bucket"
  value       = aws_s3_bucket.processed.id
}

output "jobs_queue_url" {
  description = "URL of the SQS jobs queue"
  value       = aws_sqs_queue.jobs.url
}

output "cdn_domain" {
  description = "CloudFront distribution domain"
  value       = aws_cloudfront_distribution.main.domain_name
}

output "upload_url_template" {
  description = "Template for upload URLs"
  value       = "s3://${aws_s3_bucket.raw.id}/uploads/{videoId}/{filename}"
}

output "playback_url_template" {
  description = "Template for playback URLs"
  value       = "https://${aws_cloudfront_distribution.main.domain_name}/processed/{videoId}/master.m3u8"
}

output "live_rtmp_url" {
  description = "RTMP ingest URL for live streaming"
  value       = var.enable_live && length(module.live) > 0 ? module.live[0].rtmp_url : null
}

output "dynamodb_table_name" {
  description = "DynamoDB table name"
  value       = aws_dynamodb_table.videos.name
}

