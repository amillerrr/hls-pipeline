# Security Group for the Load Balancer
resource "aws_security_group" "lb_sg" {
  name        = "eye-lb-sg"
  description = "Security group for ALB"
  vpc_id      = aws_vpc.main.id

  # Allow public HTTP traffic
  ingress {
    protocol    = "tcp"
    from_port   = 80
    to_port     = 80
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow public HTTPS traffic 
  ingress {
    protocol    = "tcp"
    from_port   = 443
    to_port     = 443
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow outbound to anywhere
  egress {
    protocol    = "-1"
    from_port   = 0
    to_port     = 0
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "eye-lb-sg"
  }
}

# Load Balancer 
resource "aws_lb" "main" {
  name               = "eye-alb"
  load_balancer_type = "application"
  security_groups    = [aws_security_group.lb_sg.id]
  subnets            = [aws_subnet.public_1.id, aws_subnet.public_2.id]
  # Enable in production
  enable_deletion_protection = false

  # access_logs {
  #   bucket  = aws_s3_bucket.lb_logs.bucket
  #   prefix  = "alb-logs"
  #   enabled = true
  # }

  tags = {
    Name = "eye-alb"
  }
}

# Target Group
resource "aws_lb_target_group" "api" {
  name        = "eye-api-tg"
  port        = 8080
  protocol    = "HTTP"
  vpc_id      = aws_vpc.main.id
  target_type = "ip"

  health_check {
    enabled             = true
    path                = "/health"
    port                = "traffic-port"
    protocol            = "HTTP"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    timeout             = 5
    interval            = 30
    matcher             = "200"
  }

  stickiness {
    type            = "lb_cookie"
    cookie_duration = 86400
    enabled         = false # Enable if you need session affinity
  }

  tags = {
    Name = "eye-api-tg"
  }
}

# HTTP Listener
resource "aws_lb_listener" "front_end_http" {
  load_balancer_arn = aws_lb.main.arn
  port              = "80"
  protocol          = "HTTP"

  default_action {
    type = "redirect"

    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

# HTTPS Listener
resource "aws_lb_listener" "front_end_https" {
  load_balancer_arn = aws_lb.main.arn
  port              = "443"
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-2016-08"
  certificate_arn   = aws_acm_certificate_validation.alb_cert.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.api.arn
  }
}

# Block external source access to /metrics
resource "aws_lb_listener_rule" "block_metrics" {
  listener_arn = aws_lb_listener.front_end_https.arn
  priority     = 1

  action {
    type = "fixed-response"

    fixed_response {
      content_type = "text/plain"
      message_body = "Forbidden"
      status_code  = "403"
    }
  }

  condition {
    path_pattern {
      values = ["/metrics"]
    }
  }
}
