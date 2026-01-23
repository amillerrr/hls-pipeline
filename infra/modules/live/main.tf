terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}

# Variables
variable "environment" {
  description = "Environment name"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID"
  type        = string
}

variable "subnet_ids" {
  description = "Subnet IDs for the ECS service"
  type        = list(string)
}

variable "cluster_id" {
  description = "ECS cluster ID"
  type        = string
}

variable "cluster_name" {
  description = "ECS cluster name"
  type        = string
}

variable "live_image" {
  description = "Docker image for the live service"
  type        = string
}

variable "mediamtx_image" {
  description = "Docker image for MediaMTX"
  type        = string
  default     = "bluenviron/mediamtx:latest"
}

variable "output_bucket" {
  description = "S3 bucket for live output"
  type        = string
}

variable "output_bucket_arn" {
  description = "ARN of the S3 output bucket"
  type        = string
}

variable "cdn_domain" {
  description = "CDN domain for output URLs"
  type        = string
  default     = ""
}

variable "cpu" {
  description = "CPU units for the task"
  type        = number
  default     = 2048
}

variable "memory" {
  description = "Memory (MB) for the task"
  type        = number
  default     = 4096
}

variable "desired_count" {
  description = "Desired number of tasks"
  type        = number
  default     = 1
}

variable "srt_port_range_start" {
  description = "Start of SRT port range"
  type        = number
  default     = 9000
}

variable "srt_port_range_end" {
  description = "End of SRT port range"
  type        = number
  default     = 9100
}

variable "rtmp_port" {
  description = "RTMP ingest port"
  type        = number
  default     = 1935
}

variable "api_port" {
  description = "API port"
  type        = number
  default     = 8080
}

variable "enable_nlb" {
  description = "Enable Network Load Balancer for SRT/RTMP"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

# Locals
locals {
  name_prefix = "hls-pipeline-live-${var.environment}"
}

# IAM Role for ECS Task Execution
resource "aws_iam_role" "task_execution" {
  name = "${local.name_prefix}-task-execution"

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

resource "aws_iam_role_policy_attachment" "task_execution" {
  role       = aws_iam_role.task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# IAM Role for ECS Task
resource "aws_iam_role" "task" {
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

resource "aws_iam_role_policy" "task_s3" {
  name = "${local.name_prefix}-s3"
  role = aws_iam_role.task.id

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
      }
    ]
  })
}

# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "live" {
  name              = "/ecs/${local.name_prefix}"
  retention_in_days = 30

  tags = var.tags
}

# Security Group
resource "aws_security_group" "live" {
  name        = "${local.name_prefix}-sg"
  description = "Security group for live streaming service"
  vpc_id      = var.vpc_id

  # API port
  ingress {
    from_port   = var.api_port
    to_port     = var.api_port
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "API"
  }

  # RTMP port
  ingress {
    from_port   = var.rtmp_port
    to_port     = var.rtmp_port
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "RTMP"
  }

  # SRT port range (UDP)
  ingress {
    from_port   = var.srt_port_range_start
    to_port     = var.srt_port_range_end
    protocol    = "udp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "SRT"
  }

  # MediaMTX API
  ingress {
    from_port   = 9997
    to_port     = 9997
    protocol    = "tcp"
    cidr_blocks = ["10.0.0.0/8"]
    description = "MediaMTX API"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-sg"
  })
}

# ECS Task Definition
resource "aws_ecs_task_definition" "live" {
  family                   = local.name_prefix
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.task.arn

  container_definitions = jsonencode([
    # MediaMTX sidecar container
    {
      name      = "mediamtx"
      image     = var.mediamtx_image
      essential = true
      
      portMappings = [
        {
          containerPort = 1935
          hostPort      = 1935
          protocol      = "tcp"
        },
        {
          containerPort = 8554
          hostPort      = 8554
          protocol      = "tcp"
        },
        {
          containerPort = 9997
          hostPort      = 9997
          protocol      = "tcp"
        }
      ]

      environment = [
        {
          name  = "MTX_PROTOCOLS"
          value = "tcp,udp"
        },
        {
          name  = "MTX_RTSPADDRESS"
          value = ":8554"
        },
        {
          name  = "MTX_RTMPADDRESS"
          value = ":1935"
        },
        {
          name  = "MTX_API"
          value = "yes"
        },
        {
          name  = "MTX_APIADDRESS"
          value = ":9997"
        }
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.live.name
          "awslogs-region"        = data.aws_region.current.name
          "awslogs-stream-prefix" = "mediamtx"
        }
      }

      healthCheck = {
        command     = ["CMD-SHELL", "wget -q -O /dev/null http://localhost:9997/v3/paths/list || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 60
      }
    },
    
    # Live service container
    {
      name      = "live"
      image     = var.live_image
      essential = true
      
      portMappings = concat(
        [
          {
            containerPort = var.api_port
            hostPort      = var.api_port
            protocol      = "tcp"
          }
        ],
        # SRT ports
        [for port in range(var.srt_port_range_start, var.srt_port_range_end + 1) : {
          containerPort = port
          hostPort      = port
          protocol      = "udp"
        }]
      )

      environment = [
        {
          name  = "PORT"
          value = tostring(var.api_port)
        },
        {
          name  = "AWS_REGION"
          value = data.aws_region.current.name
        },
        {
          name  = "OUTPUT_BUCKET"
          value = var.output_bucket
        },
        {
          name  = "CDN_DOMAIN"
          value = var.cdn_domain
        },
        {
          name  = "MEDIAMTX_URL"
          value = "localhost"
        },
        {
          name  = "MEDIAMTX_API_PORT"
          value = "9997"
        },
        {
          name  = "SRT_PORT_RANGE_START"
          value = tostring(var.srt_port_range_start)
        },
        {
          name  = "SRT_PORT_RANGE_END"
          value = tostring(var.srt_port_range_end)
        }
      ]

      dependsOn = [
        {
          containerName = "mediamtx"
          condition     = "HEALTHY"
        }
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
        command     = ["CMD-SHELL", "wget -q -O /dev/null http://localhost:${var.api_port}/health || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 30
      }
    }
  ])

  tags = var.tags
}

# Network Load Balancer (for SRT/RTMP)
resource "aws_lb" "live" {
  count = var.enable_nlb ? 1 : 0

  name               = "${local.name_prefix}-nlb"
  internal           = false
  load_balancer_type = "network"
  subnets            = var.subnet_ids

  enable_cross_zone_load_balancing = true

  tags = var.tags
}

# NLB Target Groups
resource "aws_lb_target_group" "rtmp" {
  count = var.enable_nlb ? 1 : 0

  name        = "${local.name_prefix}-rtmp"
  port        = var.rtmp_port
  protocol    = "TCP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    enabled             = true
    healthy_threshold   = 2
    unhealthy_threshold = 2
    interval            = 30
    port                = var.api_port
    protocol            = "TCP"
  }

  tags = var.tags
}

resource "aws_lb_target_group" "srt" {
  count = var.enable_nlb ? 1 : 0

  name        = "${local.name_prefix}-srt"
  port        = var.srt_port_range_start
  protocol    = "UDP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    enabled             = true
    healthy_threshold   = 2
    unhealthy_threshold = 2
    interval            = 30
    port                = var.api_port
    protocol            = "TCP"
  }

  tags = var.tags
}

# NLB Listeners
resource "aws_lb_listener" "rtmp" {
  count = var.enable_nlb ? 1 : 0

  load_balancer_arn = aws_lb.live[0].arn
  port              = var.rtmp_port
  protocol          = "TCP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.rtmp[0].arn
  }
}

resource "aws_lb_listener" "srt" {
  count = var.enable_nlb ? 1 : 0

  load_balancer_arn = aws_lb.live[0].arn
  port              = var.srt_port_range_start
  protocol          = "UDP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.srt[0].arn
  }
}

# ECS Service
resource "aws_ecs_service" "live" {
  name            = local.name_prefix
  cluster         = var.cluster_id
  task_definition = aws_ecs_task_definition.live.arn
  desired_count   = var.desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.subnet_ids
    security_groups  = [aws_security_group.live.id]
    assign_public_ip = true
  }

  dynamic "load_balancer" {
    for_each = var.enable_nlb ? [1] : []
    content {
      target_group_arn = aws_lb_target_group.rtmp[0].arn
      container_name   = "live"
      container_port   = var.rtmp_port
    }
  }

  tags = var.tags

  lifecycle {
    ignore_changes = [desired_count]
  }
}

# Data sources
data "aws_region" "current" {}

# Outputs
output "service_name" {
  description = "ECS service name"
  value       = aws_ecs_service.live.name
}

output "security_group_id" {
  description = "Security group ID"
  value       = aws_security_group.live.id
}

output "nlb_dns_name" {
  description = "NLB DNS name"
  value       = var.enable_nlb ? aws_lb.live[0].dns_name : null
}

output "rtmp_url" {
  description = "RTMP ingest URL"
  value       = var.enable_nlb ? "rtmp://${aws_lb.live[0].dns_name}:${var.rtmp_port}/live" : null
}

output "srt_base_port" {
  description = "Base SRT port"
  value       = var.srt_port_range_start
}

