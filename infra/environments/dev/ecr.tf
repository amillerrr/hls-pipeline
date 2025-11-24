resource "aws_ecr_repository" "api" {
  name                 = "eye-api"
  image_tag_mutability = "MUTABLE"
  force_delete         = true
}

resource "aws_ecr_repository" "worker" {
  name                 = "eye-worker"
  image_tag_mutability = "MUTABLE"
  force_delete         = true
}
