# Raw Video Upload Bucket
resource "aws_s3_bucket" "raw" {
  bucket = var.raw_bucket_name

  tags = merge(var.common_tags, {
    Name      = var.raw_bucket_name
    Component = "storage"
    Purpose   = "raw-uploads"
  })
}

resource "aws_s3_bucket_versioning" "raw" {
  bucket = aws_s3_bucket.raw.id
  versioning_configuration {
    status = var.enable_versioning ? "Enabled" : "Disabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "raw" {
  bucket = aws_s3_bucket.raw.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "raw" {
  bucket = aws_s3_bucket.raw.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_cors_configuration" "raw" {
  bucket = aws_s3_bucket.raw.id

  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["PUT", "POST"]
    allowed_origins = var.cors_allowed_origins
    expose_headers  = ["ETag"]
    max_age_seconds = 3600
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "raw" {
  bucket = aws_s3_bucket.raw.id

  rule {
    id     = "transition-to-ia"
    status = "Enabled"

    filter {
      prefix = "uploads/"
    }

    transition {
      days          = var.raw_bucket_lifecycle_days
      storage_class = "STANDARD_IA"
    }

    expiration {
      days = 365
    }

    noncurrent_version_expiration {
      noncurrent_days = 30
    }
  }
}

# Processed HLS Output Bucket
resource "aws_s3_bucket" "processed" {
  bucket = var.processed_bucket_name

  tags = merge(var.common_tags, {
    Name      = var.processed_bucket_name
    Component = "storage"
    Purpose   = "hls-output"
  })
}

resource "aws_s3_bucket_versioning" "processed" {
  bucket = aws_s3_bucket.processed.id
  versioning_configuration {
    status = var.enable_versioning ? "Enabled" : "Disabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "processed" {
  bucket = aws_s3_bucket.processed.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "processed" {
  bucket = aws_s3_bucket.processed.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_cors_configuration" "processed" {
  bucket = aws_s3_bucket.processed.id

  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET", "HEAD"]
    allowed_origins = var.cors_allowed_origins
    expose_headers  = ["ETag", "Content-Length", "Content-Range"]
    max_age_seconds = 86400
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "processed" {
  bucket = aws_s3_bucket.processed.id

  rule {
    id     = "transition-old-content"
    status = "Enabled"

    filter {
      prefix = ""
    }

    transition {
      days          = var.processed_bucket_lifecycle_days
      storage_class = "STANDARD_IA"
    }

    noncurrent_version_expiration {
      noncurrent_days = 7
    }
  }
}

# Ad Segments Bucket (for SSAI)
resource "aws_s3_bucket" "ad_segments" {
  count  = var.enable_ssai ? 1 : 0
  bucket = var.ad_segments_bucket_name != "" ? var.ad_segments_bucket_name : "${var.processed_bucket_name}-ads"

  tags = merge(var.common_tags, {
    Name      = var.ad_segments_bucket_name != "" ? var.ad_segments_bucket_name : "${var.processed_bucket_name}-ads"
    Component = "storage"
    Purpose   = "ad-segments"
  })
}

resource "aws_s3_bucket_versioning" "ad_segments" {
  count  = var.enable_ssai ? 1 : 0
  bucket = aws_s3_bucket.ad_segments[0].id
  versioning_configuration {
    status = "Disabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "ad_segments" {
  count  = var.enable_ssai ? 1 : 0
  bucket = aws_s3_bucket.ad_segments[0].id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "ad_segments" {
  count  = var.enable_ssai ? 1 : 0
  bucket = aws_s3_bucket.ad_segments[0].id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_cors_configuration" "ad_segments" {
  count  = var.enable_ssai ? 1 : 0
  bucket = aws_s3_bucket.ad_segments[0].id

  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET", "HEAD"]
    allowed_origins = var.cors_allowed_origins
    expose_headers  = ["ETag", "Content-Length"]
    max_age_seconds = 86400
  }
}

# CloudFront OAI for ad segments
resource "aws_cloudfront_origin_access_identity" "ads" {
  count   = var.enable_ssai ? 1 : 0
  comment = "OAI for ad segments - ${var.environment}"
}

# Bucket policy for ad segments
resource "aws_s3_bucket_policy" "ad_segments" {
  count  = var.enable_ssai ? 1 : 0
  bucket = aws_s3_bucket.ad_segments[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AllowCloudFrontAccess"
        Effect    = "Allow"
        Principal = {
          AWS = aws_cloudfront_origin_access_identity.ads[0].iam_arn
        }
        Action   = "s3:GetObject"
        Resource = "${aws_s3_bucket.ad_segments[0].arn}/*"
      },
      {
        Sid       = "AllowMediaTailorAccess"
        Effect    = "Allow"
        Principal = {
          Service = "mediatailor.amazonaws.com"
        }
        Action   = "s3:GetObject"
        Resource = "${aws_s3_bucket.ad_segments[0].arn}/*"
      }
    ]
  })
}

# Access Logs Bucket
resource "aws_s3_bucket" "logs" {
  count  = var.enable_access_logs ? 1 : 0
  bucket = var.logs_bucket_name != "" ? var.logs_bucket_name : "${var.processed_bucket_name}-logs"

  tags = merge(var.common_tags, {
    Name      = var.logs_bucket_name != "" ? var.logs_bucket_name : "${var.processed_bucket_name}-logs"
    Component = "storage"
    Purpose   = "access-logs"
  })
}

resource "aws_s3_bucket_versioning" "logs" {
  count  = var.enable_access_logs ? 1 : 0
  bucket = aws_s3_bucket.logs[0].id
  versioning_configuration {
    status = "Disabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "logs" {
  count  = var.enable_access_logs ? 1 : 0
  bucket = aws_s3_bucket.logs[0].id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "logs" {
  count  = var.enable_access_logs ? 1 : 0
  bucket = aws_s3_bucket.logs[0].id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_lifecycle_configuration" "logs" {
  count  = var.enable_access_logs ? 1 : 0
  bucket = aws_s3_bucket.logs[0].id

  rule {
    id     = "expire-old-logs"
    status = "Enabled"

    filter {
      prefix = ""
    }

    transition {
      days          = 30
      storage_class = "STANDARD_IA"
    }

    transition {
      days          = 90
      storage_class = "GLACIER"
    }

    expiration {
      days = 365
    }
  }
}

# Allow CloudFront to write logs
resource "aws_s3_bucket_acl" "logs" {
  count  = var.enable_access_logs ? 1 : 0
  bucket = aws_s3_bucket.logs[0].id
  acl    = "log-delivery-write"
  depends_on = [aws_s3_bucket_ownership_controls.logs]
}

resource "aws_s3_bucket_ownership_controls" "logs" {
  count  = var.enable_access_logs ? 1 : 0
  bucket = aws_s3_bucket.logs[0].id
  rule {
    object_ownership = "BucketOwnerPreferred"
  }
}

# Outputs
output "raw_bucket_name" {
  description = "Name of the raw uploads bucket"
  value       = aws_s3_bucket.raw.id
}

output "raw_bucket_arn" {
  description = "ARN of the raw uploads bucket"
  value       = aws_s3_bucket.raw.arn
}

output "processed_bucket_name" {
  description = "Name of the processed HLS bucket"
  value       = aws_s3_bucket.processed.id
}

output "processed_bucket_arn" {
  description = "ARN of the processed HLS bucket"
  value       = aws_s3_bucket.processed.arn
}

output "ad_segments_bucket_name" {
  description = "Name of the ad segments bucket"
  value       = var.enable_ssai ? aws_s3_bucket.ad_segments[0].id : null
}

output "ad_segments_bucket_arn" {
  description = "ARN of the ad segments bucket"
  value       = var.enable_ssai ? aws_s3_bucket.ad_segments[0].arn : null
}

output "logs_bucket_name" {
  description = "Name of the logs bucket"
  value       = var.enable_access_logs ? aws_s3_bucket.logs[0].id : null
}
