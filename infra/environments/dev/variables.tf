variable "aws_region" {
  description = "AWS Region to deploy resources into"
  type        = string
  default     = "us-west-2"
}

variable "project_name" {
  description = "Base name for resources"
  type        = string
  default     = "eye-of-storm"
}
