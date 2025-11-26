# Security Group for the Load Balancer
resource "aws_security_group" "lb_sg" {
  name   = "eye-lb-sg"
  vpc_id = aws_vpc.main.id

  # Allow public HTTP traffic
  ingress {
    protocol    = "tcp"
    from_port   = 80
    to_port     = 80
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Allow outbound to anywhere
  egress {
    protocol    = "-1"
    from_port   = 0
    to_port     = 0
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# Load Balancer 
resource "aws_lb" "main" {
  name               = "eye-alb"
  load_balancer_type = "application"
  security_groups    = [aws_security_group.lb_sg.id]
  subnets            = [aws_subnet.public_1.id, aws_subnet.public_2.id]
}

# Target Group
resource "aws_lb_target_group" "api" {
  name        = "eye-api-tg"
  port        = 8080
  protocol    = "HTTP"
  vpc_id      = aws_vpc.main.id
  target_type = "ip" 
  
  health_check {
    path = "/metrics"
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
