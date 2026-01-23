variable "bucket_name" {
  description = "Name of the S3 bucket to configure notifications for"
  type        = string
}

variable "bucket_arn" {
  description = "ARN of the S3 bucket"
  type        = string
}

variable "queue_arn" {
  description = "ARN of the SQS queue to send notifications to"
  type        = string
}

variable "queue_url" {
  description = "URL of the SQS queue"
  type        = string
}

variable "events" {
  description = "S3 events to trigger notifications"
  type        = list(string)
  default     = ["s3:ObjectCreated:*"]
}

variable "filter_prefix" {
  description = "Object key prefix filter"
  type        = string
  default     = ""
}

variable "filter_suffix" {
  description = "Object key suffix filter (e.g., .mp4)"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

