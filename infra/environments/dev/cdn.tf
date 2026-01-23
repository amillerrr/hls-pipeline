locals {
  s3_origin_id      = "S3-${var.processed_bucket_name}"
  llhls_origin_id   = "S3-${var.processed_bucket_name}-llhls"
  
  # LL-HLS query string parameters to forward
  llhls_query_strings = [
    "_HLS_msn",           # Media Sequence Number for blocking requests
    "_HLS_part",          # Partial segment number
    "_HLS_skip",          # Delta playlist updates (YES/v2)
    "_HLS_push",          # Server push hint
    "_HLS_report"         # Delivery directives report
  ]
}

# Origin Access Identity for S3
resource "aws_cloudfront_origin_access_identity" "hls_oai" {
  comment = "OAI for HLS pipeline - ${var.environment}"
}

# Cache policy for HLS manifests (.m3u8) - Short TTL for live/LL-HLS
resource "aws_cloudfront_cache_policy" "hls_manifest" {
  name        = "hls-manifest-policy-${var.environment}"
  comment     = "Cache policy for HLS/LL-HLS manifests"
  
  min_ttl     = 0
  default_ttl = 1    # 1 second default for LL-HLS
  max_ttl     = 2    # 2 seconds max

  parameters_in_cache_key_and_forwarded_to_origin {
    cookies_config {
      cookie_behavior = "none"
    }
    
    headers_config {
      header_behavior = "whitelist"
      headers {
        items = ["Origin", "Access-Control-Request-Headers", "Access-Control-Request-Method"]
      }
    }
    
    query_strings_config {
      query_string_behavior = "whitelist"
      query_strings {
        items = local.llhls_query_strings
      }
    }
    
    enable_accept_encoding_brotli = true
    enable_accept_encoding_gzip   = true
  }
}

# Cache policy for HLS segments (.ts, .m4s) - Long TTL (immutable)
resource "aws_cloudfront_cache_policy" "hls_segments" {
  name        = "hls-segments-policy-${var.environment}"
  comment     = "Cache policy for HLS/CMAF segments (immutable)"
  
  min_ttl     = 86400     # 1 day minimum
  default_ttl = 86400     # 1 day default
  max_ttl     = 31536000  # 1 year max

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

# Cache policy for init segments (init.mp4) - Long TTL
resource "aws_cloudfront_cache_policy" "hls_init" {
  name        = "hls-init-policy-${var.environment}"
  comment     = "Cache policy for CMAF init segments"
  
  min_ttl     = 86400
  default_ttl = 604800    # 1 week
  max_ttl     = 31536000  # 1 year

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

# Origin request policy for LL-HLS
resource "aws_cloudfront_origin_request_policy" "llhls" {
  name    = "llhls-origin-request-${var.environment}"
  comment = "Origin request policy for LL-HLS blocking requests"

  cookies_config {
    cookie_behavior = "none"
  }

  headers_config {
    header_behavior = "whitelist"
    headers {
      items = ["Origin", "Access-Control-Request-Headers", "Access-Control-Request-Method"]
    }
  }

  query_strings_config {
    query_string_behavior = "whitelist"
    query_strings {
      items = local.llhls_query_strings
    }
  }
}

# Response headers policy for CORS and security
resource "aws_cloudfront_response_headers_policy" "hls_cors" {
  name    = "hls-cors-security-${var.environment}"
  comment = "CORS and security headers for HLS delivery"

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
    
    access_control_max_age_sec = 86400
    origin_override            = true
  }

  security_headers_config {
    content_type_options {
      override = true
    }
    
    frame_options {
      frame_option = "DENY"
      override     = true
    }
    
    strict_transport_security {
      access_control_max_age_sec = 31536000
      include_subdomains         = true
      preload                    = true
      override                   = true
    }
    
    xss_protection {
      mode_block = true
      protection = true
      override   = true
    }
  }
}

# Main CloudFront Distribution
resource "aws_cloudfront_distribution" "hls_distribution" {
  enabled             = true
  is_ipv6_enabled     = true
  comment             = "HLS/LL-HLS Pipeline Distribution - ${var.environment}"
  default_root_object = ""
  price_class         = var.cloudfront_price_class
  
  aliases = var.custom_domain != "" ? [var.custom_domain] : []

  # S3 Origin
  origin {
    domain_name = aws_s3_bucket.processed.bucket_regional_domain_name
    origin_id   = local.s3_origin_id
    origin_path = ""

    s3_origin_config {
      origin_access_identity = aws_cloudfront_origin_access_identity.hls_oai.cloudfront_access_identity_path
    }

    origin_shield {
      enabled              = var.enable_origin_shield
      origin_shield_region = var.origin_shield_region
    }
  }

  # Default cache behavior (catch-all)
  default_cache_behavior {
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = local.s3_origin_id

    cache_policy_id            = aws_cloudfront_cache_policy.hls_segments.id
    origin_request_policy_id   = aws_cloudfront_origin_request_policy.llhls.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.hls_cors.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = true
  }

  # HLS Manifest behavior (.m3u8) - Must be first (highest priority)
  ordered_cache_behavior {
    path_pattern     = "*.m3u8"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = local.s3_origin_id

    cache_policy_id            = aws_cloudfront_cache_policy.hls_manifest.id
    origin_request_policy_id   = aws_cloudfront_origin_request_policy.llhls.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.hls_cors.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = true
  }

  # CMAF segment behavior (.m4s)
  ordered_cache_behavior {
    path_pattern     = "*.m4s"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = local.s3_origin_id

    cache_policy_id            = aws_cloudfront_cache_policy.hls_segments.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.hls_cors.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = false  # Video segments are already compressed
  }

  # TS segment behavior (.ts) - Legacy support
  ordered_cache_behavior {
    path_pattern     = "*.ts"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = local.s3_origin_id

    cache_policy_id            = aws_cloudfront_cache_policy.hls_segments.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.hls_cors.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = false
  }

  # Init segment behavior (init.mp4)
  ordered_cache_behavior {
    path_pattern     = "*init*.mp4"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = local.s3_origin_id

    cache_policy_id            = aws_cloudfront_cache_policy.hls_init.id
    response_headers_policy_id = aws_cloudfront_response_headers_policy.hls_cors.id

    viewer_protocol_policy = "redirect-to-https"
    compress               = false
  }

  # DRM license requests (if using CloudFront for license proxy)
  dynamic "ordered_cache_behavior" {
    for_each = var.enable_drm ? [1] : []
    content {
      path_pattern     = "/license/*"
      allowed_methods  = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]
      cached_methods   = ["GET", "HEAD"]
      target_origin_id = local.s3_origin_id

      cache_policy_id          = aws_cloudfront_cache_policy.hls_manifest.id
      viewer_protocol_policy   = "https-only"
      compress                 = true
    }
  }

  # Geo restrictions
  restrictions {
    geo_restriction {
      restriction_type = var.geo_restriction_type
      locations        = var.geo_restriction_locations
    }
  }

  # SSL Certificate
  viewer_certificate {
    cloudfront_default_certificate = var.custom_domain == ""
    acm_certificate_arn            = var.custom_domain != "" ? var.acm_certificate_arn : null
    ssl_support_method             = var.custom_domain != "" ? "sni-only" : null
    minimum_protocol_version       = "TLSv1.2_2021"
  }

  # Logging
  dynamic "logging_config" {
    for_each = var.enable_access_logs ? [1] : []
    content {
      include_cookies = false
      bucket          = aws_s3_bucket.logs[0].bucket_domain_name
      prefix          = "cloudfront/"
    }
  }

  tags = merge(var.common_tags, {
    Name        = "hls-distribution-${var.environment}"
    Component   = "cdn"
    Protocol    = "HLS,LL-HLS,CMAF"
  })
}

# S3 Bucket Policy to allow CloudFront access
resource "aws_s3_bucket_policy" "processed_bucket_policy" {
  bucket = aws_s3_bucket.processed.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AllowCloudFrontAccess"
        Effect    = "Allow"
        Principal = {
          AWS = aws_cloudfront_origin_access_identity.hls_oai.iam_arn
        }
        Action   = "s3:GetObject"
        Resource = "${aws_s3_bucket.processed.arn}/*"
      }
    ]
  })
}

# Outputs
output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID"
  value       = aws_cloudfront_distribution.hls_distribution.id
}

output "cloudfront_domain_name" {
  description = "CloudFront distribution domain name"
  value       = aws_cloudfront_distribution.hls_distribution.domain_name
}

output "cloudfront_hosted_zone_id" {
  description = "CloudFront hosted zone ID for Route53"
  value       = aws_cloudfront_distribution.hls_distribution.hosted_zone_id
}

output "hls_playback_url" {
  description = "Base URL for HLS playback"
  value       = "https://${var.custom_domain != "" ? var.custom_domain : aws_cloudfront_distribution.hls_distribution.domain_name}"
}
