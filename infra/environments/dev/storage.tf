# Random string to ensure global bucket uniqueness
resource "random_string" "suffix" {
  length  = 6
  special = false
  upper   = false
}

# Raw Ingest Bucket
resource "aws_s3_bucket" "raw_ingest" {
  bucket        = "eye-raw-ingest-${random_string.suffix.result}"
  force_destroy = true

  tags = {
    Name        = "eye-raw-ingest"
    Purpose     = "Raw video uploads"
    Environment = "dev"
  }
}

resource "aws_s3_bucket_versioning" "raw_ver" {
  bucket = aws_s3_bucket.raw_ingest.id
  versioning_configuration {
    status = "Enabled"
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

# Block public access for raw bucket 
resource "aws_s3_bucket_public_access_block" "raw_access" {
  bucket = aws_s3_bucket.raw_ingest.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Processed Video Bucket 
resource "aws_s3_bucket" "processed" {
  bucket        = "eye-processed-${random_string.suffix.result}"
  force_destroy = true

  tags = {
    Name        = "eye-processed"
    Purpose     = "Processed HLS streams"
    Environment = "dev"
  }
}

resource "aws_s3_bucket_public_access_block" "processed_access" {
  bucket = aws_s3_bucket.processed.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
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

resource "aws_s3_bucket_lifecycle_configuration" "raw_lifecycle" {
  bucket = aws_s3_bucket.raw_ingest.id

  rule {
    id     = "cleanup-temp"
    status = "Enabled"

    filter {
      prefix = "uploads/"
    }

    expiration {
      days = 1
    }

    noncurrent_version_expiration {
      noncurrent_days = 1
    }
  }
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

# Allow CloudFront to read from the bucket
data "aws_iam_policy_document" "cloudfront_oac_access" {
  statement {
    sid       = "AllowCloudFrontServicePrincipal"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.processed.arn}/*"]

    principals {
      type        = "Service"
      identifiers = ["cloudfront.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [aws_cloudfront_distribution.s3_distribution.arn]
    }
  }
}

resource "aws_s3_bucket_policy" "processed_policy" {
  bucket = aws_s3_bucket.processed.id
  policy = data.aws_iam_policy_document.cloudfront_oac_access.json
}
