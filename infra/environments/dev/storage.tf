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
}

resource "aws_s3_bucket_versioning" "raw_ver" {
  bucket = aws_s3_bucket.raw_ingest.id
  versioning_configuration {
    status = "Enabled"
  }
}

# Processed Video Bucket 
resource "aws_s3_bucket" "processed" {
  bucket        = "eye-processed-${random_string.suffix.result}"
  force_destroy = true
}

resource "aws_s3_bucket_public_access_block" "processed_access" {
  bucket = aws_s3_bucket.processed.id

  block_public_acls       = false
  block_public_policy     = false
  ignore_public_acls      = false
  restrict_public_buckets = false
}

resource "aws_s3_bucket_policy" "public_read" {
  bucket     = aws_s3_bucket.processed.id
  depends_on = [aws_s3_bucket_public_access_block.processed_access]

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "PublicReadGetObject"
        Effect    = "Allow"
        Principal = "*"
        Action    = "s3:GetObject"
        Resource  = "${aws_s3_bucket.processed.arn}/*"
      },
    ]
  })
}

resource "aws_s3_bucket_cors_configuration" "processed_cors" {
  bucket = aws_s3_bucket.processed.id

  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET", "HEAD"]
    allowed_origins = ["*"]
    expose_headers  = ["ETag"]
    max_age_seconds = 3000
  }
}

# Allow CloudFront to read from the bucket
data "aws_iam_policy_document" "cloudfront_oac_access" {
  statement {
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
