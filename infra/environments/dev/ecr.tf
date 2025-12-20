locals {
  ecr_repos = {
    api = {
      name        = "hls-api"
      description = "HLS Pipeline API service"
    }
    worker = {
      name        = "hls-worker"
      description = "HLS Pipeline Worker service"
    }
  }

  # Common lifecycle policy for all repositories
  ecr_lifecycle_policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep last 10 images"
        selection = {
          tagStatus     = "tagged"
          tagPrefixList = ["v"]
          countType     = "imageCountMoreThan"
          countNumber   = 10
        }
        action = {
          type = "expire"
        }
      },
      {
        rulePriority = 2
        description  = "Expire untagged images older than 7 days"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 7
        }
        action = {
          type = "expire"
        }
      },
      {
        rulePriority = 3
        description  = "Keep last 5 latest images"
        selection = {
          tagStatus     = "tagged"
          tagPrefixList = ["latest"]
          countType     = "imageCountMoreThan"
          countNumber   = 5
        }
        action = {
          type = "expire"
        }
      }
    ]
  })
}

resource "aws_ecr_repository" "repos" {
  for_each = local.ecr_repos

  name                 = each.value.name
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }

  tags = {
    Name        = each.value.name
    Description = each.value.description
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

resource "aws_ecr_lifecycle_policy" "repos" {
  for_each = local.ecr_repos

  repository = aws_ecr_repository.repos[each.key].name
  policy     = local.ecr_lifecycle_policy
}

# Outputs
output "ecr_repository_urls" {
  description = "Map of ECR repository URLs"
  value = {
    for key, repo in aws_ecr_repository.repos : key => repo.repository_url
  }
}

output "ecr_repository_arns" {
  description = "Map of ECR repository ARNs"
  value = {
    for key, repo in aws_ecr_repository.repos : key => repo.arn
  }
}

# Individual outputs for backwards compatibility
output "api_repository_url" {
  description = "ECR repository URL for API"
  value       = aws_ecr_repository.repos["api"].repository_url
}

output "worker_repository_url" {
  description = "ECR repository URL for Worker"
  value       = aws_ecr_repository.repos["worker"].repository_url
}
