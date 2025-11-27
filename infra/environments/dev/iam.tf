# Worker Policy
resource "aws_iam_policy" "worker_policy" {
  name        = "eye-worker-policy"
  description = "Allows worker to access S3, SQS, X-Ray, and CloudWatch"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "S3RawAccess"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject"
        ]
        Resource = "${aws_s3_bucket.raw_ingest.arn}/*"
      },
      {
        Sid    = "S3ProcessedAccess"
        Effect = "Allow"
        Action = [
          "s3:PutObject",
          "s3:PutObjectAcl",
          "s3:ListBucket",
          "s3:GetObject"
        ]
        Resource = "${aws_s3_bucket.processed.arn}/*"
      },
      {
        Sid    = "S3ProcessedListAccess"
        Effect = "Allow"
        Action = [
          "s3:ListBucket"
        ]
        Resource = aws_s3_bucket.processed.arn
      },
      {
        Sid    = "SQSAccess"
        Effect = "Allow"
        Action = [
          "sqs:ReceiveMessage",
          "sqs:SendMessage",
          "sqs:DeleteMessage",
          "sqs:GetQueueAttributes",
          "sqs:GetQueueUrl"
        ]
        Resource = aws_sqs_queue.video_queue.arn
      },
      {
        Sid    = "AllowXRayWrites"
        Effect = "Allow"
        Action = [
          "xray:PutTraceSegments",
          "xray:PutTelemetryRecords",
          "xray:GetSamplingRules",
          "xray:GetSamplingTargets",
          "xray:GetSamplingStatisticSummaries"
        ]
        Resource = "*"
      },
      {
        Sid    = "AllowCloudWatchLogs"
        Effect = "Allow"
        Action = [
          "logs:PutLogEvents",
          "logs:CreateLogStream",
          "logs:CreateLogGroup",
          "logs:DescribeLogStreams",
          "logs:DescribeLogGroups"
        ]
        Resource = [
          "arn:aws:logs:${var.aws_region}:*:log-group:/aws/ecs/*",
          "arn:aws:logs:${var.aws_region}:*:log-group:/ecs/*",
          "arn:aws:logs:${var.aws_region}:*:log-group:/metrics/*"
        ]
      },
      {
        Sid    = "AllowCloudWatchMetrics"
        Effect = "Allow"
        Action = [
          "cloudwatch:PutMetricData"
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "cloudwatch:namespace" = "EyeOfTheStorm"
          }
        }
      }
    ]
  })
}

# API Policy
resource "aws_iam_policy" "api_policy" {
  name        = "eye-api-policy"
  description = "Allows API to access S3, SQS, X-Ray, and CloudWatch"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "S3RawUpload"
        Effect = "Allow"
        Action = [
          "s3:PutObject"
        ]
        Resource = "${aws_s3_bucket.raw_ingest.arn}/uploads/*"
      },
      {
        Sid    = "S3ProcessedList"
        Effect = "Allow"
        Action = [
          "s3:ListBucket"
        ]
        Resource = aws_s3_bucket.processed.arn
      },
      {
        Sid    = "SQSSendOnly"
        Effect = "Allow"
        Action = [
          "sqs:SendMessage",
          "sqs:GetQueueUrl"
        ]
        Resource = aws_sqs_queue.video_queue.arn
      },
      {
        Sid    = "AllowXRayWrites"
        Effect = "Allow"
        Action = [
          "xray:PutTraceSegments",
          "xray:PutTelemetryRecords",
          "xray:GetSamplingRules",
          "xray:GetSamplingTargets"
        ]
        Resource = "*"
      },
      {
        Sid    = "AllowCloudWatchLogs"
        Effect = "Allow"
        Action = [
          "logs:PutLogEvents",
          "logs:CreateLogStream"
        ]
        Resource = "arn:aws:logs:${var.aws_region}:*:log-group:/ecs/*:*"
      },
      {
        Sid    = "AllowCloudWatchMetrics"
        Effect = "Allow"
        Action = [
          "cloudwatch:PutMetricData"
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "cloudwatch:namespace" = "EyeOfTheStorm"
          }
        }
      }
    ]
  })
}

# Trust Policy
data "aws_iam_policy_document" "ecs_assume_role" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

# Execution Role
resource "aws_iam_role" "ecs_execution_role" {
  name               = "eye-ecs-execution-role"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json

  tags = {
    Name = "eye-ecs-execution-role"
  }
}

resource "aws_iam_role_policy_attachment" "ecs_execution_attach" {
  role       = aws_iam_role.ecs_execution_role.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# Task Role
resource "aws_iam_role" "ecs_task_role" {
  name               = "eye-ecs-task-role"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json

  tags = {
    Name = "eye-ecs-task-role"
  }
}

# Attach api and worker policy to the Task Role
resource "aws_iam_role" "api_task_role" {
  name               = "eye-api-task-role"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json
}

resource "aws_iam_role_policy_attachment" "api_task_attach" {
  role       = aws_iam_role.api_task_role.name
  policy_arn = aws_iam_policy.api_policy.arn
}

resource "aws_iam_role" "worker_task_role" {
  name               = "eye-worker-task-role"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume_role.json
}

resource "aws_iam_role_policy_attachment" "worker_task_attach" {
  role       = aws_iam_role.worker_task_role.name
  policy_arn = aws_iam_policy.worker_policy.arn
}
