# HLS Pipeline Infrastructure - Main Configuration

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }

  # Backend configuration is in versions.tf
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

# Local Variables

locals {
  name_prefix = "hls-pipeline-${var.environment}"

  common_tags = {
    Project     = "hls-pipeline"
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

# Modules

# S3 Event Notifications -> SQS (Event-Driven Architecture)
module "s3_events" {
  source = "../../modules/s3-events"

  bucket_name   = aws_s3_bucket.raw.id
  bucket_arn    = aws_s3_bucket.raw.arn
  queue_arn     = aws_sqs_queue.jobs.arn
  queue_url     = aws_sqs_queue.jobs.url

  # Filter to only process video uploads
  filter_prefix = "uploads/"

  # Optionally filter by file extension (empty = all files)
  # filter_suffix = ".mp4"

  events = ["s3:ObjectCreated:*"]

  tags = local.common_tags
}

# Live Streaming Infrastructure (Optional)
module "live" {
  source = "../../modules/live"
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

  tags = local.common_tags
}
