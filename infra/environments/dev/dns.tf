data "aws_route53_zone" "main" {
  name         = var.root_domain
  private_zone = false
}

locals {
  api_domain = "api.${var.subdomain_label}.${var.root_domain}"
  cdn_domain = "${var.subdomain_label}.${var.root_domain}"
}

# ALB certificate 
resource "aws_acm_certificate" "alb_cert" {
  domain_name       = local.api_domain
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_route53_record" "alb_cert_val" {
  for_each = {
    for dvo in aws_acm_certificate.alb_cert.domain_validation_options : dvo.domain_name => dvo
  }

  allow_overwrite = true
  name            = each.value.resource_record_name
  records         = [each.value.resource_record_value]
  ttl             = 60
  type            = each.value.resource_record_type
  zone_id         = data.aws_route53_zone.main.zone_id
}

resource "aws_acm_certificate_validation" "alb_cert" {
  certificate_arn         = aws_acm_certificate.alb_cert.arn
  validation_record_fqdns = [for record in aws_route53_record.alb_cert_val : record.fqdn]
}

# CloudFront certificate
resource "aws_acm_certificate" "cdn_cert" {
  domain_name       = local.cdn_domain
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_route53_record" "cdn_cert_val" {
  provider = aws.us_east_1
  for_each = {
    for dvo in aws_acm_certificate.cdn_cert.domain_validation_options : dvo.domain_name => dvo
  }

  allow_overwrite = true
  name            = each.value.resource_record_name
  records         = [each.value.resource_record_value]
  ttl             = 60
  type            = each.value.resource_record_type
  zone_id         = data.aws_route53_zone.main.zone_id
}

resource "aws_acm_certificate_validation" "cdn_cert" {
  provider                = aws.us_east_1
  certificate_arn         = aws_acm_certificate.cdn_cert.arn
  validation_record_fqdns = [for record in aws_route53_record.cdn_cert_val : record.fqdn]
}

# DNS Records for the Services
resource "aws_route53_record" "api" {
  zone_id = data.aws_route53_zone.main.zone_id
  name    = local.api_domain
  type    = "A"

  alias {
    name                   = aws_lb.main.dns_name
    zone_id                = aws_lb.main.zone_id
    evaluate_target_health = true
  }
}

resource "aws_route53_record" "cdn" {
  zone_id = data.aws_route53_zone.main.zone_id
  name    = local.cdn_domain
  type    = "A"

  alias {
    name                   = aws_cloudfront_distribution.s3_distribution.domain_name
    zone_id                = aws_cloudfront_distribution.s3_distribution.hosted_zone_id
    evaluate_target_health = false
  }
}
