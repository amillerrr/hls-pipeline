resource "aws_cloudfront_cache_policy" "llhls" {
  name        = "hls-llhls-cache-policy"
  min_ttl     = 0
  default_ttl = 1 # Very short TTL for playlist updates
  max_ttl     = 31536000

  parameters_in_cache_key_and_forwarded_to_origin {
    cookies_config {
      cookie_behavior = "none"
    }
    headers_config {
      header_behavior = "none"
    }
    query_strings_config {
      query_string_behavior = "whitelist"
      query_strings {
        items = [
          "_HLS_msn",  # Media Sequence Number
          "_HLS_part", # Part number
          "_HLS_skip"  # Skip directive
        ]
      }
    }
  }
}

resource "aws_cloudfront_origin_request_policy" "llhls" {
  name = "hls-llhls-origin-request"

  cookies_config {
    cookie_behavior = "none"
  }
  headers_config {
    header_behavior = "whitelist"
    headers {
      items = ["Origin", "Access-Control-Request-Method"]
    }
  }
  query_strings_config {
    query_string_behavior = "whitelist"
    query_strings {
      items = ["_HLS_msn", "_HLS_part", "_HLS_skip"]
    }
  }
}

# Cache behavior for LL-HLS playlists
resource "aws_cloudfront_distribution" "s3_distribution" {
  # ... existing config ...

  # LL-HLS playlist behavior (short TTL, query string forwarding)
  ordered_cache_behavior {
    path_pattern     = "*.m3u8"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "S3-${aws_s3_bucket.processed.bucket}"

    cache_policy_id          = aws_cloudfront_cache_policy.llhls.id
    origin_request_policy_id = aws_cloudfront_origin_request_policy.llhls.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = true
  }

  # Segment behavior (longer TTL, immutable)
  ordered_cache_behavior {
    path_pattern     = "*.m4s"
    allowed_methods  = ["GET", "HEAD"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "S3-${aws_s3_bucket.processed.bucket}"

    forwarded_values {
      query_string = false
      cookies {
        forward = "none"
      }
    }

    min_ttl                = 86400
    default_ttl            = 86400
    max_ttl                = 31536000
    viewer_protocol_policy = "redirect-to-https"
    compress               = false # Video already compressed
  }
}
