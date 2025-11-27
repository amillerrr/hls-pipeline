resource "aws_dynamodb_table" "terraform_lock" {
  name         = "eye-tf-lock-table"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"

  attribute {
    name = "LockID"
    type = "S"
  }

  tags = {
    Name = "Terraform State Lock Table"
  }
}

resource "aws_s3_bucket" "terraform_state" {
  bucket = "eye-tf-state-store"
  
  tags = {
    Name = "Terraform State Store"
  }
}

resource "aws_s3_bucket_versioning" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id
  versioning_configuration {
    status = "Enabled"
  }
}
