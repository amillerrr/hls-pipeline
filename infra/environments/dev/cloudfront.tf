# Origin Access Control
resource "aws_cloudfront_origin_access_control" "processed" {
  name                              = "${local.name_prefix}-processed-oac"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

# Response Headers Policy (CORS)
resource "aws_cloudfront_response_headers_policy" "cors" {
  name = "${local.name_prefix}-cors-policy"

  cors_config {
    access_control_allow_credentials = false

    access_control_allow_headers {
      items = ["*"]
    }

    access_control_allow_methods {
      items = ["GET", "HEAD", "OPTIONS"]
    }

    access_control_allow_origins {
      items = var.cors_allowed_origins
    }

    access_control_expose_headers {
      items = ["ETag", "Content-Length", "Content-Type", "Accept-Ranges"]
    }

    access_control_max_age_sec = 86400 # 24 hours

    origin_override = true
  }
}

# Cache Policies

# Cache policy for HLS manifests (short TTL for live)
resource "aws_cloudfront_cache_policy" "manifests" {
  name        = "${local.name_prefix}-manifests"
  min_ttl     = 0
  default_ttl = 2  # 2 seconds for live manifests
  max_ttl     = 10

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
        items = ["_HLS_msn", "_HLS_part", "_HLS_skip"]
      }
    }
    enable_accept_encoding_brotli = true
    enable_accept_encoding_gzip   = true
  }
}

# Cache policy for init segments (long TTL - immutable)
resource "aws_cloudfront_cache_policy" "init_segments" {
  name        = "${local.name_prefix}-init-segments"
  min_ttl     = 31536000 # 1 year
  default_ttl = 31536000
  max_ttl     = 31536000

  parameters_in_cache_key_and_forwarded_to_origin {
    cookies_config {
      cookie_behavior = "none"
    }
    headers_config {
      header_behavior = "none"
    }
    query_strings_config {
      query_string_behavior = "none"
    }
    enable_accept_encoding_brotli = true
    enable_accept_encoding_gzip   = true
  }
}

# Cache policy for media segments (medium TTL)
resource "aws_cloudfront_cache_policy" "segments" {
  name        = "${local.name_prefix}-segments"
  min_ttl     = 0
  default_ttl = 86400  # 24 hours
  max_ttl     = 31536000

  parameters_in_cache_key_and_forwarded_to_origin {
    cookies_config {
      cookie_behavior = "none"
    }
    headers_config {
      header_behavior = "none"
    }
    query_strings_config {
      query_string_behavior = "none"
    }
    enable_accept_encoding_brotli = true
    enable_accept_encoding_gzip   = true
  }
}

# CloudFront Distribution
resource "aws_cloudfront_distribution" "main" {
  enabled             = true
  is_ipv6_enabled     = true
  comment             = "HLS Pipeline CDN - ${var.environment}"
  default_root_object = ""
  price_class         = var.cdn_price_class
  http_version        = "http2and3"

  origin {
    domain_name              = aws_s3_bucket.processed.bucket_regional_domain_name
    origin_id                = "processed-bucket"
    origin_access_control_id = aws_cloudfront_origin_access_control.processed.id
  }

  # Default cache behavior (media segments)
  default_cache_behavior {
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "processed-bucket"

    cache_policy_id            = aws_cloudfront_cache_policy.segments.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.cors.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = true
  }

  # Cache behavior for master playlists
  ordered_cache_behavior {
    path_pattern     = "*/master.m3u8"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "processed-bucket"

    cache_policy_id            = aws_cloudfront_cache_policy.manifests.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.cors.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = true
  }

  # Cache behavior for variant playlists (LL-HLS support)
  ordered_cache_behavior {
    path_pattern     = "*/playlist.m3u8"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "processed-bucket"

    cache_policy_id            = aws_cloudfront_cache_policy.manifests.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.cors.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = true
  }

  # Cache behavior for DASH manifests
  ordered_cache_behavior {
    path_pattern     = "*.mpd"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "processed-bucket"

    cache_policy_id            = aws_cloudfront_cache_policy.manifests.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.cors.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = true
  }

  # Cache behavior for init segments (immutable)
  ordered_cache_behavior {
    path_pattern     = "*/init*.mp4"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "processed-bucket"

    cache_policy_id            = aws_cloudfront_cache_policy.init_segments.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.cors.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = false
  }

  # Cache behavior for I-frame playlists
  ordered_cache_behavior {
    path_pattern     = "*/iframe*.m3u8"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "processed-bucket"

    cache_policy_id            = aws_cloudfront_cache_policy.manifests.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.cors.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = true
  }

  restrictions {
    geo_restriction {
      restriction_type = var.cdn_geo_restriction_type
      locations        = var.cdn_geo_restriction_locations
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = var.cdn_certificate_arn == "" ? true : false
    acm_certificate_arn            = var.cdn_certificate_arn != "" ? var.cdn_certificate_arn : null
    ssl_support_method             = var.cdn_certificate_arn != "" ? "sni-only" : null
    minimum_protocol_version       = "TLSv1.2_2021"
  }

  # Custom error responses
  custom_error_response {
    error_code            = 403
    error_caching_min_ttl = 10
    response_code         = 404
    response_page_path    = ""
  }

  custom_error_response {
    error_code            = 404
    error_caching_min_ttl = 10
  }

  tags = {
    Name = "${local.name_prefix}-cdn"
  }
}
