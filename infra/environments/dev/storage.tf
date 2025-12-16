# Random string to ensure global bucket uniqueness
resource "random_string" "suffix" {
  length  = 6
  special = false
  upper   = false
}

# Raw Ingest Bucket
resource "aws_s3_bucket" "raw_ingest" {
  bucket        = "hls-raw-ingest-${random_string.suffix.result}"
  force_destroy = true

  tags = {
    Name        = "hls-raw-ingest"
    Environment = var.environment
    Application = "hls-pipeline"
  }
}

# Enable server-side encryption for raw bucket
resource "aws_s3_bucket_server_side_encryption_configuration" "raw_encryption" {
  bucket = aws_s3_bucket.raw_ingest.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_versioning" "raw_ver" {
  bucket = aws_s3_bucket.raw_ingest.id
  versioning_configuration {
    status = "Enabled"
  }
}

# Block public access for raw bucket 
resource "aws_s3_bucket_public_access_block" "raw_access" {
  bucket = aws_s3_bucket.raw_ingest.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Lifecycle rule to delete raw uploads after processing
resource "aws_s3_bucket_lifecycle_configuration" "raw_ingest_lifecycle" {
  bucket = aws_s3_bucket.raw_ingest.id

  rule {
    id     = "delete-old-uploads"
    status = "Enabled"

    filter {
      prefix = "uploads/"
    }

    expiration {
      days = 1 # Delete after 1 day
    }

    noncurrent_version_expiration {
      noncurrent_days = 1
    }
  }
}

# Processed Video Bucket 
resource "aws_s3_bucket" "processed" {
  bucket        = "hls-processed-${random_string.suffix.result}"
  force_destroy = true

  tags = {
    Name        = "hls-processed"
    Environment = var.environment
    Application = "hls-pipeline"
  }
}

# Enable server-side encryption for processed bucket
resource "aws_s3_bucket_server_side_encryption_configuration" "processed_encryption" {
  bucket = aws_s3_bucket.processed.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_public_access_block" "processed_access" {
  bucket = aws_s3_bucket.processed.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Lifecycle for processed bucket to move old content to cheaper storage
resource "aws_s3_bucket_lifecycle_configuration" "processed_lifecycle" {
  bucket = aws_s3_bucket.processed.id

  rule {
    id     = "archive-old-videos"
    status = "Enabled"

    filter {
      prefix = ""
    }

    transition {
      days          = 30
      storage_class = "INTELLIGENT_TIERING"
    }

    # Move to Glacier after 90 days (if you want long-term archival)
    # transition {
    #   days          = 90
    #   storage_class = "GLACIER"
    # }
  }
}

resource "aws_s3_bucket_cors_configuration" "processed_cors" {
  bucket = aws_s3_bucket.processed.id

  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET", "HEAD"]
    allowed_origins = ["*"]
    expose_headers  = ["ETag", "Content-Length", "Content-Type"]
    max_age_seconds = 3000
  }
}

# Bucket policy for CloudFront access
resource "aws_s3_bucket_policy" "processed_cloudfront" {
  bucket = aws_s3_bucket.processed.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid    = "AllowCloudFrontAccessServicePrincipal"
      Effect = "Allow"
      Principal = {
        Service = "cloudfront.amazonaws.com"
      }
      Action   = "s3:GetObject"
      Resource = "${aws_s3_bucket.processed.arn}/*"
      Condition = {
        StringEquals = {
          "AWS:SourceArn" = aws_cloudfront_distribution.s3_distribution.arn
        }
      }
    }]
  })
}

# Outputs
output "raw_ingest_bucket_name" {
  description = "Name of the raw ingest S3 bucket"
  value       = aws_s3_bucket.raw_ingest.bucket
}

output "raw_ingest_bucket_arn" {
  description = "ARN of the raw ingest S3 bucket"
  value       = aws_s3_bucket.raw_ingest.arn
}

output "processed_bucket_name" {
  description = "Name of the processed S3 bucket"
  value       = aws_s3_bucket.processed.bucket
}

output "processed_bucket_arn" {
  description = "ARN of the processed S3 bucket"
  value       = aws_s3_bucket.processed.arn
}

