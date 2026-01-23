locals {
  name_prefix = "hls-pipeline-${var.environment}-live"
}

data "aws_region" "current" {}

# Security Group
resource "aws_security_group" "live" {
  name        = "${local.name_prefix}-sg"
  description = "Security group for live streaming service"
  vpc_id      = var.vpc_id

  # SRT ingest
  ingress {
    from_port   = 9998
    to_port     = 9999
    protocol    = "udp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "SRT ingest"
  }

  # RTMP ingest
  ingress {
    from_port   = 1935
    to_port     = 1935
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "RTMP ingest"
  }

  # Health check / API
  ingress {
    from_port   = 8080
    to_port     = 8080
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Health check"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow all outbound"
  }

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-sg"
  })
}

# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "live" {
  name              = "/ecs/${local.name_prefix}"
  retention_in_days = 30
  tags              = var.tags
}

# IAM Role for Live Tasks
resource "aws_iam_role" "live_task" {
  name = "${local.name_prefix}-task"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "ecs-tasks.amazonaws.com"
        }
      }
    ]
  })

  tags = var.tags
}

resource "aws_iam_role_policy" "live_task" {
  name = "${local.name_prefix}-task-policy"
  role = aws_iam_role.live_task.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:PutObject",
          "s3:GetObject",
          "s3:DeleteObject",
          "s3:ListBucket"
        ]
        Resource = [
          var.output_bucket_arn,
          "${var.output_bucket_arn}/*"
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
          "dynamodb:Query",
          "dynamodb:Scan"
        ]
        Resource = [
          var.dynamodb_table_arn,
          "${var.dynamodb_table_arn}/index/*"
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "cloudwatch:PutMetricData",
          "xray:PutTraceSegments",
          "xray:PutTelemetryRecords"
        ]
        Resource = "*"
      }
    ]
  })
}

# ECS Task Definition
resource "aws_ecs_task_definition" "live" {
  family                   = local.name_prefix
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = var.execution_role_arn
  task_role_arn            = aws_iam_role.live_task.arn

  container_definitions = jsonencode([
    {
      name  = "live"
      image = var.live_image

      portMappings = [
        { containerPort = 9998, hostPort = 9998, protocol = "udp" },
        { containerPort = 9999, hostPort = 9999, protocol = "udp" },
        { containerPort = 1935, hostPort = 1935, protocol = "tcp" },
        { containerPort = 8080, hostPort = 8080, protocol = "tcp" }
      ]

      environment = [
        { name = "OUTPUT_BUCKET", value = var.output_bucket },
        { name = "CDN_DOMAIN", value = var.cdn_domain },
        { name = "ENVIRONMENT", value = var.environment },
        { name = "DYNAMODB_TABLE", value = var.dynamodb_table_name },
        { name = "AWS_REGION", value = data.aws_region.current.name }
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.live.name
          "awslogs-region"        = data.aws_region.current.name
          "awslogs-stream-prefix" = "live"
        }
      }

      healthCheck = {
        command     = ["CMD-SHELL", "curl -f http://localhost:8080/health || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 60
      }

      essential = true
    }
  ])

  tags = var.tags
}

# ECS Service
resource "aws_ecs_service" "live" {
  name                               = local.name_prefix
  cluster                            = var.cluster_id
  task_definition                    = aws_ecs_task_definition.live.arn
  desired_count                      = var.desired_count
  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200

  capacity_provider_strategy {
    capacity_provider = "FARGATE"
    weight            = 1
  }

  network_configuration {
    subnets          = var.subnet_ids
    security_groups  = [aws_security_group.live.id]
    assign_public_ip = true
  }

  dynamic "load_balancer" {
    for_each = var.enable_nlb ? [1] : []
    content {
      target_group_arn = aws_lb_target_group.srt[0].arn
      container_name   = "live"
      container_port   = 9998
    }
  }

  lifecycle {
    ignore_changes = [desired_count]
  }

  tags = var.tags
}

# Network Load Balancer (Optional)
resource "aws_lb" "live" {
  count = var.enable_nlb ? 1 : 0

  name                             = "${local.name_prefix}-nlb"
  internal                         = false
  load_balancer_type               = "network"
  subnets                          = var.subnet_ids
  enable_cross_zone_load_balancing = true

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-nlb"
  })
}

resource "aws_lb_target_group" "srt" {
  count = var.enable_nlb ? 1 : 0

  name        = "${local.name_prefix}-srt"
  port        = 9998
  protocol    = "UDP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    protocol            = "TCP"
    port                = 8080
    healthy_threshold   = 2
    unhealthy_threshold = 2
    interval            = 30
  }

  tags = var.tags
}

resource "aws_lb_listener" "srt" {
  count = var.enable_nlb ? 1 : 0

  load_balancer_arn = aws_lb.live[0].arn
  port              = 9998
  protocol          = "UDP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.srt[0].arn
  }
}

resource "aws_lb_target_group" "rtmp" {
  count = var.enable_nlb ? 1 : 0

  name        = "${local.name_prefix}-rtmp"
  port        = 1935
  protocol    = "TCP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    protocol            = "TCP"
    port                = 8080
    healthy_threshold   = 2
    unhealthy_threshold = 2
    interval            = 30
  }

  tags = var.tags
}

resource "aws_lb_listener" "rtmp" {
  count = var.enable_nlb ? 1 : 0

  load_balancer_arn = aws_lb.live[0].arn
  port              = 1935
  protocol          = "TCP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.rtmp[0].arn
  }
}
