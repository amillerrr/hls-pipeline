variable "environment" {
  description = "Environment name"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID"
  type        = string
}

variable "subnet_ids" {
  description = "Subnet IDs for ECS tasks"
  type        = list(string)
}

variable "cluster_id" {
  description = "ECS cluster ID"
  type        = string
}

variable "cluster_name" {
  description = "ECS cluster name"
  type        = string
}

variable "live_image" {
  description = "Docker image for live streaming service"
  type        = string
}

variable "output_bucket" {
  description = "S3 bucket name for output"
  type        = string
}

variable "output_bucket_arn" {
  description = "S3 bucket ARN for output"
  type        = string
}

variable "cdn_domain" {
  description = "CloudFront domain for playback URLs"
  type        = string
}

variable "cpu" {
  description = "Task CPU units"
  type        = number
  default     = 2048
}

variable "memory" {
  description = "Task memory in MB"
  type        = number
  default     = 4096
}

variable "desired_count" {
  description = "Desired number of tasks"
  type        = number
  default     = 1
}

variable "enable_nlb" {
  description = "Enable Network Load Balancer for ingest"
  type        = bool
  default     = false
}

variable "execution_role_arn" {
  description = "ECS task execution role ARN"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}
