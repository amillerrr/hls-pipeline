# AWS Configuration
variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "us-west-2"
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
  default     = "dev"
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

# S3 Configuration
variable "raw_bucket_expiration_days" {
  description = "Days to keep raw uploads before deletion"
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
  description = "SQS message visibility timeout"
  type        = number
  default     = 3600 # 1 hour
}

variable "sqs_message_retention_days" {
  description = "SQS message retention in days"
  type        = number
  default     = 7
}

variable "sqs_receive_wait_time_seconds" {
  description = "SQS long polling wait time"
  type        = number
  default     = 20
}

variable "sqs_max_receive_count" {
  description = "Max receives before sending to DLQ"
  type        = number
  default     = 3
}

# DynamoDB Configuration
variable "dynamodb_billing_mode" {
  description = "DynamoDB billing mode (PAY_PER_REQUEST or PROVISIONED)"
  type        = string
  default     = "PAY_PER_REQUEST"
}

# CDN Configuration
variable "cdn_price_class" {
  description = "CloudFront price class"
  type        = string
  default     = "PriceClass_100"
}

# Live Streaming Configuration
variable "enable_live" {
  description = "Enable live streaming infrastructure"
  type        = bool
  default     = false
}

variable "live_image" {
  description = "Docker image for live service"
  type        = string
  default     = ""
}

variable "live_cpu" {
  description = "CPU units for live service"
  type        = number
  default     = 2048
}

variable "live_memory" {
  description = "Memory (MB) for live service"
  type        = number
  default     = 4096
}

variable "live_desired_count" {
  description = "Desired count for live service"
  type        = number
  default     = 1
}

variable "enable_live_nlb" {
  description = "Enable Network Load Balancer for live ingest"
  type        = bool
  default     = true
}

