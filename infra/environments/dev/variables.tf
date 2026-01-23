# Core AWS Configuration
variable "aws_region" {
  description = "AWS region for resources"
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
  default     = "dev"

  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "Environment must be dev, staging, or prod."
  }
}

# Networking
variable "vpc_id" {
  description = "VPC ID for ECS services"
  type        = string
}

variable "subnet_ids" {
  description = "Subnet IDs for ECS services"
  type        = list(string)
}

variable "assign_public_ip" {
  description = "Assign public IP to ECS tasks (required if no NAT gateway)"
  type        = bool
  default     = true
}

# S3 Configuration
variable "raw_bucket_expiration_days" {
  description = "Days before raw uploads expire"
  type        = number
  default     = 7
}

variable "cors_allowed_origins" {
  description = "Allowed origins for CORS"
  type        = list(string)
  default     = ["*"]
}

# SQS Configuration
variable "sqs_visibility_timeout_seconds" {
  description = "SQS message visibility timeout (should be > max job duration)"
  type        = number
  default     = 900 # 15 minutes
}

variable "sqs_message_retention_days" {
  description = "SQS message retention in days"
  type        = number
  default     = 4
}

variable "sqs_receive_wait_time_seconds" {
  description = "SQS long polling wait time"
  type        = number
  default     = 20
}

variable "sqs_max_receive_count" {
  description = "Max receive count before sending to DLQ"
  type        = number
  default     = 3
}

variable "sqs_alarm_threshold" {
  description = "Queue depth threshold for CloudWatch alarm"
  type        = number
  default     = 100
}

# DynamoDB Configuration
variable "dynamodb_billing_mode" {
  description = "DynamoDB billing mode (PAY_PER_REQUEST or PROVISIONED)"
  type        = string
  default     = "PAY_PER_REQUEST"
}

variable "dynamodb_ttl_enabled" {
  description = "Enable TTL for automatic record cleanup"
  type        = bool
  default     = false
}

variable "dynamodb_read_min_capacity" {
  description = "Minimum read capacity (only for PROVISIONED mode)"
  type        = number
  default     = 5
}

variable "dynamodb_read_max_capacity" {
  description = "Maximum read capacity (only for PROVISIONED mode)"
  type        = number
  default     = 100
}

variable "dynamodb_write_min_capacity" {
  description = "Minimum write capacity (only for PROVISIONED mode)"
  type        = number
  default     = 5
}

variable "dynamodb_write_max_capacity" {
  description = "Maximum write capacity (only for PROVISIONED mode)"
  type        = number
  default     = 100
}

# ECS Configuration
variable "enable_container_insights" {
  description = "Enable CloudWatch Container Insights"
  type        = bool
  default     = true
}

variable "use_spot_instances" {
  description = "Use Fargate Spot for cost savings"
  type        = bool
  default     = false
}

variable "enable_ecs_exec" {
  description = "Enable ECS Exec for debugging"
  type        = bool
  default     = true
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 30
}

# API Service
variable "api_cpu" {
  description = "API task CPU units (256, 512, 1024, 2048, 4096)"
  type        = number
  default     = 512
}

variable "api_memory" {
  description = "API task memory in MB"
  type        = number
  default     = 1024
}

variable "api_desired_count" {
  description = "API service desired task count"
  type        = number
  default     = 2
}

# Worker Service
variable "worker_cpu" {
  description = "Worker task CPU units"
  type        = number
  default     = 2048
}

variable "worker_memory" {
  description = "Worker task memory in MB"
  type        = number
  default     = 4096
}

variable "worker_desired_count" {
  description = "Worker service desired task count"
  type        = number
  default     = 1
}

variable "worker_min_count" {
  description = "Worker service minimum task count"
  type        = number
  default     = 1
}

variable "worker_max_count" {
  description = "Worker service maximum task count"
  type        = number
  default     = 10
}

variable "worker_max_concurrent_jobs" {
  description = "Max concurrent jobs per worker"
  type        = number
  default     = 2
}

# CloudFront Configuration
variable "cdn_price_class" {
  description = "CloudFront price class"
  type        = string
  default     = "PriceClass_100" # North America and Europe only

  validation {
    condition     = contains(["PriceClass_100", "PriceClass_200", "PriceClass_All"], var.cdn_price_class)
    error_message = "Must be PriceClass_100, PriceClass_200, or PriceClass_All."
  }
}

variable "cdn_certificate_arn" {
  description = "ACM certificate ARN for custom domain (empty = use CloudFront default)"
  type        = string
  default     = ""
}

variable "cdn_geo_restriction_type" {
  description = "Geo restriction type (none, whitelist, blacklist)"
  type        = string
  default     = "none"
}

variable "cdn_geo_restriction_locations" {
  description = "Country codes for geo restriction"
  type        = list(string)
  default     = []
}

# Secrets
variable "jwt_secret_arn" {
  description = "ARN of JWT secret in Secrets Manager"
  type        = string
  default     = ""
}

# Live Streaming (Optional)

variable "enable_live" {
  description = "Enable live streaming infrastructure"
  type        = bool
  default     = false
}

variable "live_image" {
  description = "Docker image for live streaming service"
  type        = string
  default     = ""
}

variable "live_cpu" {
  description = "Live service CPU units"
  type        = number
  default     = 2048
}

variable "live_memory" {
  description = "Live service memory in MB"
  type        = number
  default     = 4096
}

variable "live_desired_count" {
  description = "Live service desired task count"
  type        = number
  default     = 1
}

variable "enable_live_nlb" {
  description = "Enable NLB for live streaming ingest"
  type        = bool
  default     = false
}

# Feature Flags
variable "use_lambda_for_s3_events" {
  description = "Use Lambda instead of direct S3->SQS for event processing"
  type        = bool
  default     = false
}
