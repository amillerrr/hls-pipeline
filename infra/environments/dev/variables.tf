# Environment Configuration
variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
  default     = "dev"

  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "Environment must be dev, staging, or prod."
  }
}

variable "aws_region" {
  description = "AWS region for resources"
  type        = string
  default     = "us-east-1"
}

variable "common_tags" {
  description = "Common tags to apply to all resources"
  type        = map(string)
  default = {
    Project   = "hls-pipeline"
    ManagedBy = "terraform"
  }
}

# S3 Bucket Configuration
variable "raw_bucket_name" {
  description = "Name of the S3 bucket for raw video uploads"
  type        = string
}

variable "processed_bucket_name" {
  description = "Name of the S3 bucket for processed HLS output"
  type        = string
}

variable "ad_segments_bucket_name" {
  description = "Name of the S3 bucket for ad segments (SSAI)"
  type        = string
  default     = ""
}

variable "logs_bucket_name" {
  description = "Name of the S3 bucket for access logs"
  type        = string
  default     = ""
}

variable "enable_versioning" {
  description = "Enable versioning on S3 buckets"
  type        = bool
  default     = true
}

variable "raw_bucket_lifecycle_days" {
  description = "Days before raw uploads are transitioned to IA storage"
  type        = number
  default     = 30
}

variable "processed_bucket_lifecycle_days" {
  description = "Days before processed content is transitioned to IA storage"
  type        = number
  default     = 90
}

# CloudFront Configuration
variable "cloudfront_price_class" {
  description = "CloudFront price class"
  type        = string
  default     = "PriceClass_100"

  validation {
    condition     = contains(["PriceClass_100", "PriceClass_200", "PriceClass_All"], var.cloudfront_price_class)
    error_message = "Invalid CloudFront price class."
  }
}

variable "custom_domain" {
  description = "Custom domain for CloudFront distribution"
  type        = string
  default     = ""
}

variable "acm_certificate_arn" {
  description = "ACM certificate ARN for custom domain"
  type        = string
  default     = ""
}

variable "cors_allowed_origins" {
  description = "List of allowed origins for CORS"
  type        = list(string)
  default     = ["*"]
}

variable "enable_origin_shield" {
  description = "Enable CloudFront Origin Shield"
  type        = bool
  default     = true
}

variable "origin_shield_region" {
  description = "Region for CloudFront Origin Shield"
  type        = string
  default     = "us-east-1"
}

variable "enable_access_logs" {
  description = "Enable CloudFront access logging"
  type        = bool
  default     = true
}

variable "geo_restriction_type" {
  description = "Geo restriction type (none, whitelist, blacklist)"
  type        = string
  default     = "none"
}

variable "geo_restriction_locations" {
  description = "List of country codes for geo restriction"
  type        = list(string)
  default     = []
}

# DRM Configuration
variable "enable_drm" {
  description = "Enable DRM protection"
  type        = bool
  default     = false
}

variable "drm_provider" {
  description = "DRM provider (buydrm, pallycon, axinom, local)"
  type        = string
  default     = "local"

  validation {
    condition     = contains(["buydrm", "pallycon", "axinom", "local"], var.drm_provider)
    error_message = "Invalid DRM provider."
  }
}

variable "drm_key_server_url" {
  description = "URL for DRM key server (CPIX endpoint)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "drm_api_key" {
  description = "API key for DRM provider"
  type        = string
  default     = ""
  sensitive   = true
}

variable "widevine_license_url" {
  description = "Widevine license server URL"
  type        = string
  default     = ""
}

variable "fairplay_license_url" {
  description = "FairPlay license server URL"
  type        = string
  default     = ""
}

variable "playready_license_url" {
  description = "PlayReady license server URL"
  type        = string
  default     = ""
}

variable "fairplay_certificate_url" {
  description = "FairPlay certificate URL"
  type        = string
  default     = ""
}

# SSAI / MediaTailor Configuration
variable "enable_ssai" {
  description = "Enable Server-Side Ad Insertion"
  type        = bool
  default     = false
}

variable "ad_decision_server_url" {
  description = "Ad Decision Server (ADS) URL for VAST/VMAP"
  type        = string
  default     = ""
}

variable "preroll_ad_server_url" {
  description = "Preroll ad server URL"
  type        = string
  default     = ""
}

variable "mediatailor_slate_ad_url" {
  description = "Slate ad URL for unfilled ad breaks"
  type        = string
  default     = ""
}

variable "mediatailor_personalization_threshold" {
  description = "Personalization threshold percentage (0-100)"
  type        = number
  default     = 100
}

variable "mediatailor_max_duration" {
  description = "Maximum duration for ad breaks in seconds"
  type        = number
  default     = 120
}

# SQS Configuration
variable "sqs_queue_name" {
  description = "Name of the SQS queue for processing jobs"
  type        = string
  default     = "hls-processing-queue"
}

variable "sqs_visibility_timeout" {
  description = "SQS visibility timeout in seconds"
  type        = number
  default     = 900 # 15 minutes
}

variable "sqs_message_retention" {
  description = "SQS message retention in seconds"
  type        = number
  default     = 1209600 # 14 days
}

variable "sqs_dlq_max_receives" {
  description = "Max receives before sending to DLQ"
  type        = number
  default     = 3
}

# DynamoDB Configuration
variable "dynamodb_table_name" {
  description = "Name of the DynamoDB table for video metadata"
  type        = string
  default     = "hls-videos"
}

variable "dynamodb_billing_mode" {
  description = "DynamoDB billing mode (PROVISIONED or PAY_PER_REQUEST)"
  type        = string
  default     = "PAY_PER_REQUEST"
}

variable "dynamodb_read_capacity" {
  description = "DynamoDB read capacity units (only for PROVISIONED)"
  type        = number
  default     = 5
}

variable "dynamodb_write_capacity" {
  description = "DynamoDB write capacity units (only for PROVISIONED)"
  type        = number
  default     = 5
}

# Lambda / ECS Configuration
variable "worker_memory" {
  description = "Memory allocation for worker in MB"
  type        = number
  default     = 2048
}

variable "worker_cpu" {
  description = "CPU allocation for worker"
  type        = number
  default     = 1024
}

variable "worker_timeout" {
  description = "Worker timeout in seconds"
  type        = number
  default     = 900
}

variable "worker_desired_count" {
  description = "Desired number of worker instances"
  type        = number
  default     = 1
}

variable "worker_max_count" {
  description = "Maximum number of worker instances for autoscaling"
  type        = number
  default     = 10
}

# Transcoding Configuration
variable "transcode_presets" {
  description = "Video transcoding presets (resolution:bitrate:audio_bitrate)"
  type        = list(string)
  default = [
    "1920x1080:5000k:192k",
    "1280x720:2800k:128k",
    "854x480:1400k:128k",
    "640x360:800k:96k",
    "426x240:400k:64k"
  ]
}

variable "segment_duration" {
  description = "HLS segment duration in seconds"
  type        = number
  default     = 4
}

variable "part_duration" {
  description = "LL-HLS part duration in seconds"
  type        = number
  default     = 0.5
}

variable "enable_llhls" {
  description = "Enable Low-Latency HLS output"
  type        = bool
  default     = true
}

variable "enable_cmaf" {
  description = "Enable CMAF (fMP4) segments instead of MPEG-TS"
  type        = bool
  default     = true
}

# API Configuration
variable "api_rate_limit" {
  description = "API rate limit (requests per second)"
  type        = number
  default     = 100
}

variable "api_burst_limit" {
  description = "API burst limit"
  type        = number
  default     = 200
}

variable "jwt_secret" {
  description = "JWT signing secret"
  type        = string
  sensitive   = true
}

variable "jwt_expiration_hours" {
  description = "JWT token expiration in hours"
  type        = number
  default     = 24
}

# Monitoring Configuration
variable "enable_xray" {
  description = "Enable AWS X-Ray tracing"
  type        = bool
  default     = true
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 30
}

variable "alarm_email" {
  description = "Email address for CloudWatch alarms"
  type        = string
  default     = ""
}

# Multi-CDN Configuration
variable "enable_multi_cdn" {
  description = "Enable multi-CDN routing"
  type        = bool
  default     = false
}

variable "cdn_providers" {
  description = "List of CDN providers with weights"
  type = list(object({
    name       = string
    base_url   = string
    weight     = number
    priority   = number
    health_url = string
  }))
  default = []
}

variable "cdn_failover_threshold" {
  description = "Number of failures before CDN failover"
  type        = number
  default     = 3
}

variable "cdn_health_check_interval" {
  description = "CDN health check interval in seconds"
  type        = number
  default     = 30
}
