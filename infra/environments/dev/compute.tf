locals {
  otel_collector_image = "public.ecr.aws/aws-observability/aws-otel-collector:v0.46.0"

  # API-specific OTEL config
  api_otel_config = {
    receivers = {
      otlp = {
        protocols = {
          grpc = { endpoint = "0.0.0.0:4317" }
          http = { endpoint = "0.0.0.0:4318" }
        }
      }
      prometheus = {
        config = {
          scrape_configs = [{
            job_name        = "hls-api"
            scrape_interval = "10s"
            static_configs  = [{ targets = ["localhost:8080"] }]
          }]
        }
      }
    }
    exporters = {
      awsxray = { region = var.aws_region }
      awsemf = {
        region                  = var.aws_region
        namespace               = "HLSPipeline"
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

  # Worker-specific OTEL config
  worker_otel_config = {
    receivers = {
      otlp = {
        protocols = {
          grpc = { endpoint = "0.0.0.0:4317" }
          http = { endpoint = "0.0.0.0:4318" }
        }
      }
      prometheus = {
        config = {
          scrape_configs = [{
            job_name        = "hls-worker"
            scrape_interval = "10s"
            static_configs  = [{ targets = ["localhost:2112"] }]
          }]
        }
      }
    }
    exporters = {
      awsxray = { region = var.aws_region }
      awsemf = {
        region                  = var.aws_region
        namespace               = "HLSPipeline"
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
}

# Secrets Manager for sensitive configuration
resource "aws_secretsmanager_secret" "api_credentials" {
  name                    = "hls-pipeline/api-credentials-${var.environment}"
  description             = "API credentials for HLS Pipeline"
  recovery_window_in_days = var.environment == "prod" ? 30 : 0

  tags = {
    Name        = "hls-api-credentials"
    Environment = var.environment
    Application = "hls-pipeline"
  }
}

resource "aws_secretsmanager_secret_version" "api_credentials" {
  secret_id = aws_secretsmanager_secret.api_credentials.id
  secret_string = jsonencode({
    API_USERNAME = var.api_username
    API_PASSWORD = var.api_password
    JWT_SECRET   = var.jwt_secret
  })

  lifecycle {
    ignore_changes = [secret_string]
  }
}

# Security Group for the api and worker
resource "aws_security_group" "task_sg" {
  name        = "hls-task-sg"
  description = "Security group for ECS tasks"
  vpc_id      = aws_vpc.main.id

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

  tags = {
    Name = "hls-task-sg"
  }
}

# ECS Cluster 
resource "aws_ecs_cluster" "main" {
  name = "hls-cluster"

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_cloudwatch_log_group" "logs" {
  name              = "/ecs/hls-logs"
  retention_in_days = 7

  tags = {
    Application = "hls-pipeline"
  }
}

# Task Definitions

# API Task
resource "aws_ecs_task_definition" "api" {
  family                   = "hls-api"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = 256
  memory                   = 512
  execution_role_arn       = aws_iam_role.ecs_execution_role.arn
  task_role_arn            = aws_iam_role.api_task_role.arn

  container_definitions = jsonencode([
    {
      name      = "api"
      image     = "${aws_ecr_repository.api.repository_url}:latest"
      essential = true
      dependsOn = [{ containerName = "aws-otel-collector", condition = "START" }]
      portMappings = [{
        containerPort = 8080
        protocol      = "tcp"
      }]
      environment = [
        { name = "AWS_REGION", value = var.aws_region },
        { name = "S3_BUCKET", value = aws_s3_bucket.raw_ingest.bucket },
        { name = "SQS_QUEUE_URL", value = aws_sqs_queue.video_queue.id },
        { name = "OTEL_EXPORTER_OTLP_ENDPOINT", value = "http://localhost:4317" },
        { name = "PROCESSED_BUCKET", value = aws_s3_bucket.processed.bucket },
        { name = "CDN_DOMAIN", value = "${var.subdomain_label}.${var.root_domain}" },
        { name = "DYNAMODB_TABLE", value = aws_dynamodb_table.videos.name },
        { name = "ENV", value = var.environment }
      ]
      secrets = [
        {
          name      = "API_USERNAME"
          valueFrom = "${aws_secretsmanager_secret.api_credentials.arn}:API_USERNAME::"
        },
        {
          name      = "API_PASSWORD"
          valueFrom = "${aws_secretsmanager_secret.api_credentials.arn}:API_PASSWORD::"
        },
        {
          name      = "JWT_SECRET"
          valueFrom = "${aws_secretsmanager_secret.api_credentials.arn}:JWT_SECRET::"
        }
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.logs.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "api"
        }
      }
      healthCheck = {
        command     = ["CMD-SHELL", "wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 60
      }
    },
    {
      name      = "aws-otel-collector"
      image     = local.otel_collector_image
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
  family                   = "hls-worker"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = 1024
  memory                   = 2048
  execution_role_arn       = aws_iam_role.ecs_execution_role.arn
  task_role_arn            = aws_iam_role.worker_task_role.arn

  container_definitions = jsonencode([
    {
      name      = "worker"
      image     = "${aws_ecr_repository.worker.repository_url}:latest"
      essential = true
      dependsOn = [{ containerName = "aws-otel-collector", condition = "START" }]
      environment = [
        { name = "AWS_REGION", value = var.aws_region },
        { name = "S3_BUCKET", value = aws_s3_bucket.raw_ingest.bucket },
        { name = "PROCESSED_BUCKET", value = aws_s3_bucket.processed.bucket },
        { name = "SQS_QUEUE_URL", value = aws_sqs_queue.video_queue.id },
        { name = "DYNAMODB_TABLE", value = aws_dynamodb_table.videos.name },
        { name = "OTEL_EXPORTER_OTLP_ENDPOINT", value = "http://localhost:4317" },
        { name = "MAX_CONCURRENT_JOBS", value = "1" },
        { name = "ENV", value = var.environment }

      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.logs.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "worker"
        }
      }
      healthCheck = {
        command     = ["CMD-SHELL", "wget --no-verbose --tries=1 --spider http://localhost:2112/health || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 60
      }
    },
    {
      name      = "aws-otel-collector"
      image     = local.otel_collector_image
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

# Services

resource "aws_ecs_service" "api" {
  name                              = "hls-api-svc"
  cluster                           = aws_ecs_cluster.main.id
  task_definition                   = aws_ecs_task_definition.api.arn
  desired_count                     = 1
  wait_for_steady_state             = false
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

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }
}

resource "aws_ecs_service" "worker" {
  name                              = "hls-worker-svc"
  cluster                           = aws_ecs_cluster.main.id
  task_definition                   = aws_ecs_task_definition.worker.arn
  desired_count                     = 1
  wait_for_steady_state             = false
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

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }
}

# Auto Scaling

resource "aws_appautoscaling_target" "worker" {
  max_capacity       = 10
  min_capacity       = 1
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.worker.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

# CloudWatch Metric Query
resource "aws_cloudwatch_metric_alarm" "worker_backlog" {
  alarm_name          = "hls-worker-backlog-scale-out"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  threshold           = 0

  # Trigger scaling action
  alarm_actions = [aws_appautoscaling_policy.worker_scale_out.arn]
  ok_actions    = [aws_appautoscaling_policy.worker_scale_in.arn]

  metric_query {
    id          = "e1"
    expression  = "visible + invisible"
    label       = "TotalBacklog"
    return_data = true
  }

  metric_query {
    id = "visible"
    metric {
      metric_name = "ApproximateNumberOfMessagesVisible"
      namespace   = "AWS/SQS"
      period      = 60
      stat        = "Average"
      dimensions = {
        QueueName = aws_sqs_queue.video_queue.name
      }
    }
  }

  metric_query {
    id = "invisible"
    metric {
      metric_name = "ApproximateNumberOfMessagesNotVisible"
      namespace   = "AWS/SQS"
      period      = 60
      stat        = "Average"
      dimensions = {
        QueueName = aws_sqs_queue.video_queue.name
      }
    }
  }
}

# Scale OUT Policy 
resource "aws_appautoscaling_policy" "worker_scale_out" {
  name               = "worker-scale-out"
  policy_type        = "StepScaling"
  resource_id        = aws_appautoscaling_target.worker.resource_id
  scalable_dimension = aws_appautoscaling_target.worker.scalable_dimension
  service_namespace  = aws_appautoscaling_target.worker.service_namespace

  step_scaling_policy_configuration {
    adjustment_type         = "ChangeInCapacity"
    cooldown                = 60
    metric_aggregation_type = "Average"

    step_adjustment {
      metric_interval_lower_bound = 0
      metric_interval_upper_bound = 10
      scaling_adjustment          = 1 # Add 1 worker if backlog > 0
    }

    step_adjustment {
      metric_interval_lower_bound = 10
      scaling_adjustment          = 2 # Add 2 workers if backlog > 10
    }
  }
}

# Scale IN Policy 
resource "aws_appautoscaling_policy" "worker_scale_in" {
  name               = "worker-scale-in"
  policy_type        = "StepScaling"
  resource_id        = aws_appautoscaling_target.worker.resource_id
  scalable_dimension = aws_appautoscaling_target.worker.scalable_dimension
  service_namespace  = aws_appautoscaling_target.worker.service_namespace

  step_scaling_policy_configuration {
    adjustment_type         = "ChangeInCapacity"
    cooldown                = 300 # Wait 5 minutes before scaling in
    metric_aggregation_type = "Average"

    step_adjustment {
      metric_interval_upper_bound = 0
      scaling_adjustment          = -1 # Remove 1 worker if backlog is 0
    }
  }
}

# Scale based on CPU utilization as a secondary metric
resource "aws_appautoscaling_policy" "worker_cpu_scaling" {
  name               = "worker-cpu-scaling"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.worker.resource_id
  scalable_dimension = aws_appautoscaling_target.worker.scalable_dimension
  service_namespace  = aws_appautoscaling_target.worker.service_namespace

  target_tracking_scaling_policy_configuration {
    target_value       = 70.0
    scale_in_cooldown  = 300
    scale_out_cooldown = 60

    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
  }
}

# Auto scaling for API for high traffic scenarios
resource "aws_appautoscaling_target" "api" {
  max_capacity       = 5
  min_capacity       = 1
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.api.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "api_cpu_scaling" {
  name               = "api-cpu-scaling"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.api.resource_id
  scalable_dimension = aws_appautoscaling_target.api.scalable_dimension
  service_namespace  = aws_appautoscaling_target.api.service_namespace

  target_tracking_scaling_policy_configuration {
    target_value       = 70.0
    scale_in_cooldown  = 300
    scale_out_cooldown = 60

    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
  }
}
