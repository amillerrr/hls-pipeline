resource "aws_media_tailor_playback_configuration" "hls_ads" {
  name = "hls-pipeline-ssai"

  ad_decision_server_url = var.ad_decision_server_url

  cdn_configuration {
    ad_segment_url_prefix      = "https://${aws_cloudfront_distribution.ads.domain_name}"
    content_segment_url_prefix = "https://${var.subdomain_label}.${var.root_domain}"
  }

  manifest_processing_rules {
    ad_marker_passthrough {
      enabled = true
    }
  }

  # SCTE-35 marker configuration
  live_pre_roll_configuration {
    ad_decision_server_url = var.preroll_ad_server_url
    max_duration_seconds   = 30
  }

  tags = {
    Environment = var.environment
    Application = "hls-pipeline"
  }
}

# Dedicated CloudFront for ad segments
resource "aws_cloudfront_distribution" "ads" {
  origin {
    domain_name = aws_s3_bucket.ad_segments.bucket_regional_domain_name
    origin_id   = "ad-segments"

    s3_origin_config {
      origin_access_identity = aws_cloudfront_origin_access_identity.ads.cloudfront_access_identity_path
    }
  }

  enabled = true
  comment = "Ad segment delivery"

  default_cache_behavior {
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    target_origin_id       = "ad-segments"
    viewer_protocol_policy = "redirect-to-https"

    forwarded_values {
      query_string = false
      cookies {
        forward = "none"
      }
    }

    # Short TTL for ad personalization
    min_ttl     = 0
    default_ttl = 300
    max_ttl     = 600
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = true
  }
}
