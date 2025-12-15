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
  default     = "video"
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

# Sensitive variables 
# TF_VAR_api_username, TF_VAR_api_password, TF_VAR_jwt_secret

variable "api_username" {
  description = "Username for API authentication"
  type        = string
  default     = "admin"
  sensitive   = true
}

variable "api_password" {
  description = "Password for API authentication"
  type        = string
  sensitive   = true
  nullable    = false

  validation {
    condition     = length(var.api_password) >= 12
    error_message = "API password must be at least 12 characters long."
  }
}

variable "jwt_secret" {
  description = "Secret key for JWT token signing (min 32 characters)"
  type        = string
  sensitive   = true
  nullable    = false

  validation {
    condition     = length(var.jwt_secret) >= 32
    error_message = "JWT secret must be at least 32 characters long."
  }
}
