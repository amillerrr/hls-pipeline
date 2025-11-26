provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = "EyeOfTheStorm"
      Environment = "Dev"
      Owner       = "PipelineMentor"
      ManagedBy   = "Terraform"
    }
  }
}

# Alias for CloudFront Certs
provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"
}
