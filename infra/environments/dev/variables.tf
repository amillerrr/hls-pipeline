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

variable "root_domain" {
  description = "The root domain name"
  type        = string
  default     = "miller.today"
}

variable "subdomain_label" {
  description = "The subdomain prefix"
  type        = string
  default     = "toptal"
}
