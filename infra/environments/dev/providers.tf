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
