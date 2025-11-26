locals {
  base_otel = {
    receivers = {
      otlp = {
        protocols = {
          grpc = { endpoint = "0.0.0.0:4317" } 
          http = { endpoint = "0.0.0.0:4318" }
        }
      }
      prometheus = {
        config = {
          scrape_configs = [
            {
              job_name        = "eye-api"
              scrape_interval = "10s"
              static_configs  = [{ targets = ["localhost:8080"] }]
            },
            {
              job_name        = "eye-worker"
              scrape_interval = "10s"
              static_configs  = [{ targets = ["localhost:2112"] }]
            }
          ]
        }
      }
    }
    exporters = {
      awsxray = { region = var.aws_region }
      awsemf  = {
        region    = var.aws_region
        namespace = "EyeOfTheStorm"
        dimension_rollup_option = "NoDimensionRollup"
      }
    }
    service = {
      pipelines = {
        traces  = { receivers = ["otlp"], exporters = ["awsxray"] }
        metrics = { receivers = ["prometheus"], exporters = ["awsemf"] }
      }
    }
  }
  api_otel_config = merge(local.base_otel, {
    receivers = merge(local.base_otel.receivers, {
      prometheus = {
        config = {
          scrape_configs = [{
            job_name        = "eye-api"
            scrape_interval = "10s"
            static_configs  = [{ targets = ["localhost:8080"] }]
          }]
        }
      }
    })
  })
  worker_otel_config = merge(local.base_otel, {
    receivers = merge(local.base_otel.receivers, {
      prometheus = {
        config = {
          scrape_configs = [{
            job_name        = "eye-worker"
            scrape_interval = "10s"
            static_configs  = [{ targets = ["localhost:2112"] }]
          }]
        }
      }
    })
  })
}

# Security Group for the api and worker
resource "aws_security_group" "task_sg" {
  name   = "eye-task-sg"
  vpc_id = aws_vpc.main.id

  # Allow Traffic only from the Load Balancer
  ingress {
    protocol        = "tcp"
    from_port       = 8080
    to_port         = 8080
    security_groups = [aws_security_group.lb_sg.id] 
  }

  # Allow all outbound
  egress {
    protocol    = "-1"
    from_port   = 0
    to_port     = 0
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# --- ECS Cluster ---
resource "aws_ecs_cluster" "main" {
  name = "eye-cluster"
}

resource "aws_cloudwatch_log_group" "logs" {
  name              = "/ecs/eye-logs"
  retention_in_days = 7
}

# --- Task Definitions ---

# API Task
resource "aws_ecs_task_definition" "api" {
  family                   = "eye-api"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = 256
  memory                   = 512
  execution_role_arn       = aws_iam_role.ecs_execution_role.arn
  task_role_arn            = aws_iam_role.ecs_task_role.arn

  container_definitions = jsonencode([
    {
      name  = "api"
      image = "${aws_ecr_repository.api.repository_url}:latest"
      dependsOn = [{ containerName = "aws-otel-collector", condition = "START" }]
      portMappings = [{ containerPort = 8080 }]
      environment = [
        { name = "AWS_REGION", value = var.aws_region },
        { name = "S3_BUCKET", value = aws_s3_bucket.raw_ingest.bucket },
        { name = "SQS_QUEUE_URL", value = aws_sqs_queue.video_queue.id },
        { name = "OTEL_EXPORTER_OTLP_ENDPOINT", value = "http://localhost:4317" },
        { name = "PROCESSED_BUCKET", value = aws_s3_bucket.processed.bucket },
        { name = "CDN_DOMAIN", value = "${var.subdomain_label}.${var.root_domain}" }
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.logs.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "api"
        }
      }
    },
    {
      name      = "aws-otel-collector"
      image     = "public.ecr.aws/aws-observability/aws-otel-collector:latest"
      cpu       = 0
      essential = true
      environment = [
        {
          name  = "AOT_CONFIG_CONTENT"
          value = yamlencode(local.api_otel_config)
        }
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.logs.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "otel-sidecar-api"
        }
      }
    }
  ])
  lifecycle {
    ignore_changes = [container_definitions]
  }
}

# Worker Task
resource "aws_ecs_task_definition" "worker" {
  family                   = "eye-worker"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = 1024
  memory                   = 2048
  execution_role_arn       = aws_iam_role.ecs_execution_role.arn
  task_role_arn            = aws_iam_role.ecs_task_role.arn

  container_definitions = jsonencode([
    {
      name  = "worker"
      image = "${aws_ecr_repository.worker.repository_url}:latest"
      dependsOn = [{ containerName = "aws-otel-collector", condition = "START" }]
      environment = [
        { name = "AWS_REGION", value = var.aws_region },
        { name = "S3_BUCKET", value = aws_s3_bucket.raw_ingest.bucket },
        { name = "PROCESSED_BUCKET", value = aws_s3_bucket.processed.bucket },
        { name = "SQS_QUEUE_URL", value = aws_sqs_queue.video_queue.id },
        { name = "OTEL_EXPORTER_OTLP_ENDPOINT", value = "http://localhost:4317" }
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.logs.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "worker"
        }
      }
    },
    {
      name      = "aws-otel-collector"
      image     = "public.ecr.aws/aws-observability/aws-otel-collector:latest"
      cpu       = 0
      essential = true
      environment = [
        {
          name  = "AOT_CONFIG_CONTENT"
          value = yamlencode(local.worker_otel_config)
        }
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.logs.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "otel-sidecar-worker"
        }
      }
    }
  ])
  lifecycle {
    ignore_changes = [container_definitions]
  }
}

# --- Services ---

resource "aws_ecs_service" "api" {
  name            = "eye-api-svc"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.api.arn
  desired_count   = 1
  wait_for_steady_state = false
  health_check_grace_period_seconds = 60

  capacity_provider_strategy {
    capacity_provider = "FARGATE"
    base              = 1
    weight            = 0
  }

  capacity_provider_strategy {
    capacity_provider = "FARGATE_SPOT"
    weight            = 1
  }

  network_configuration {
    subnets          = [aws_subnet.public_1.id, aws_subnet.public_2.id]
    security_groups  = [aws_security_group.task_sg.id]
    assign_public_ip = true
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.api.arn
    container_name   = "api"
    container_port   = 8080
  }
}

resource "aws_ecs_service" "worker" {
  name            = "eye-worker-svc"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.worker.arn
  desired_count   = 1
  wait_for_steady_state = false
  health_check_grace_period_seconds = 60

  capacity_provider_strategy {
    capacity_provider = "FARGATE_SPOT"
    weight            = 100 
  }

  network_configuration {
    subnets          = [aws_subnet.public_1.id, aws_subnet.public_2.id]
    security_groups  = [aws_security_group.task_sg.id]
    assign_public_ip = true
  }
}
