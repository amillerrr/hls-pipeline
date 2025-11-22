# Random string to ensure global bucket uniqueness
resource "random_string" "suffix" {
  length  = 6
  special = false
  upper   = false
}

# A. Raw Ingest Bucket (Private)
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

# B. Processed Video Bucket (Public Read)
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
