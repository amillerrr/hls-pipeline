variable "aws_region" {
  description = "AWS Region to deploy resources into"
  type        = string
  default     = "us-west-2"
}

variable "project_name" {
  description = "Base name for resources"
  type        = string
  default     = "hls-pipeline"
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

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
  default     = "dev"
}

variable "github_org" {
  description = "GitHub organization name for OIDC"
  type        = string
  default     = "amillerrr"
}

variable "github_repo" {
  description = "GitHub repository name for OIDC"
  type        = string
  default     = "hls-pipeline"
}

variable "alert_email" {
  description = "Email address for CloudWatch alarm notifications"
  type        = string
  default     = "andrew@mill3r.la"
}
