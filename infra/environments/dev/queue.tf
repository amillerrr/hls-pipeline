resource "aws_sqs_queue" "video_queue" {
  name                       = "eye-video-queue-${var.environment}"
  delay_seconds              = 0
  max_message_size           = 262144
  message_retention_seconds  = 86400   
  receive_wait_time_seconds  = 20
  visibility_timeout_seconds = 900

  sqs_managed_sse_enabled = true

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.video_dlq.arn
    maxReceiveCount     = 3
  })

  tags = {
    Name        = "eye-video-queue"
    Environment = var.environment
    Application = "eye-of-storm"
  }
}

# Dead Letter Queue
resource "aws_sqs_queue" "video_dlq" {
  name                      = "eye-video-dlq-${var.environment}"
  message_retention_seconds = 1209600  # 14 days

  # FIXED: Added encryption
  sqs_managed_sse_enabled = true

  tags = {
    Name        = "eye-video-dlq"
    Environment = var.environment
    Application = "eye-of-storm"
  }
}

# SNS Topic for alerts
resource "aws_sns_topic" "alerts" {
  name = "eye-alerts-${var.environment}"

  tags = {
    Name        = "eye-alerts"
    Environment = var.environment
  }
}

resource "aws_sns_topic_subscription" "alerts_email" {
  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "email"
  endpoint  = var.alert_email
}

# DLQ alarm with action
resource "aws_cloudwatch_metric_alarm" "dlq_messages" {
  alarm_name          = "eye-dlq-messages-${var.environment}"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  alarm_description   = "Alert when messages land in DLQ"
  treat_missing_data  = "notBreaching"

  dimensions = {
    QueueName = aws_sqs_queue.video_dlq.name
  }

  alarm_actions = [aws_sns_topic.alerts.arn]
  ok_actions    = [aws_sns_topic.alerts.arn]

  tags = {
    Name        = "eye-dlq-alarm"
    Environment = var.environment
  }
}

# Queue depth alarm
resource "aws_cloudwatch_metric_alarm" "queue_depth" {
  alarm_name          = "eye-queue-depth-${var.environment}"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Average"
  threshold           = 50
  alarm_description   = "Alert when queue depth exceeds 50 messages"
  treat_missing_data  = "notBreaching"

  dimensions = {
    QueueName = aws_sqs_queue.video_queue.name
  }

  alarm_actions = [aws_sns_topic.alerts.arn]

  tags = {
    Name        = "eye-queue-depth-alarm"
    Environment = var.environment
  }
}

# Messages age alarm
resource "aws_cloudwatch_metric_alarm" "message_age" {
  alarm_name          = "eye-message-age-${var.environment}"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateAgeOfOldestMessage"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 3600  # 1 hour
  alarm_description   = "Alert when oldest message is over 1 hour old"
  treat_missing_data  = "notBreaching"

  dimensions = {
    QueueName = aws_sqs_queue.video_queue.name
  }

  alarm_actions = [aws_sns_topic.alerts.arn]

  tags = {
    Name        = "eye-message-age-alarm"
    Environment = var.environment
  }
}

# Outputs
output "video_queue_url" {
  description = "URL of the video processing SQS queue"
  value       = aws_sqs_queue.video_queue.id
}

output "video_queue_arn" {
  description = "ARN of the video processing SQS queue"
  value       = aws_sqs_queue.video_queue.arn
}

output "video_dlq_url" {
  description = "URL of the dead letter queue"
  value       = aws_sqs_queue.video_dlq.id
}

output "video_dlq_arn" {
  description = "ARN of the dead letter queue"
  value       = aws_sqs_queue.video_dlq.arn
}

output "alerts_topic_arn" {
  description = "ARN of the SNS alerts topic"
  value       = aws_sns_topic.alerts.arn
}

