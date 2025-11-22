provider "aws" {
  region = "us-west-2"
}

resource "aws_s3_bucket" "raw_video_bucket" {
  bucket = "aetherion-eye-raw-ingest"
  
  # Prevent accidental deletion of data
  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_versioning" "raw_versioning" {
  bucket = aws_s3_bucket.raw_video_bucket.id
  versioning_configuration {
    status = "Enabled"
  }
}
