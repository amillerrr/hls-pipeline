resource "aws_sqs_queue" "video_dlq" {
  name                      = "video-transcode-dlq"
  message_retention_seconds = 1209600 # 14 days

  sqs_managed_sse_enabled = true

  tags = {
    Name    = "video-transcode-dlq"
    Purpose = "Failed transcode job storage"
  }
}

# Main processing queue
resource "aws_sqs_queue" "video_queue" {
  name                       = "video-transcode-queue"
  delay_seconds              = 0
  max_message_size           = 2048
  message_retention_seconds  = 86400 # 1 day
  receive_wait_time_seconds  = 10
  visibility_timeout_seconds = 960
  sqs_managed_sse_enabled    = true

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.video_dlq.arn
    maxReceiveCount     = 3
  })

  tags = {
    Name    = "video-transcode-queue"
    Purpose = "Video processing job queue"
  }
}

# SNS Topic for alerts
resource "aws_sns_topic" "alerts" {
  name = "eye-alerts"

  tags = {
    Name = "eye-alerts"
  }
}

resource "aws_sns_topic_subscription" "alert_email" {
  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "email"
  endpoint  = "andrew@mill3r.la"
}

resource "aws_cloudwatch_metric_alarm" "dlq_depth" {
  alarm_name          = "eye-dlq-depth-alarm"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  alarm_description   = "Alert when jobs are failing and landing in DLQ"
  treat_missing_data  = "notBreaching"

  dimensions = {
    QueueName = aws_sqs_queue.video_dlq.name
  }

  alarm_actions = [aws_sns_topic.alerts.arn]
  ok_actions    = [aws_sns_topic.alerts.arn]

  tags = {
    Name = "eye-dlq-depth-alarm"
  }
}

# Queue depth too high alarm
resource "aws_cloudwatch_metric_alarm" "queue_depth_high" {
  alarm_name          = "eye-queue-depth-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Average"
  threshold           = 50
  alarm_description   = "Alert when queue has too many pending jobs"
  treat_missing_data  = "notBreaching"

  dimensions = {
    QueueName = aws_sqs_queue.video_queue.name
  }

  alarm_actions = [aws_sns_topic.alerts.arn]

  tags = {
    Name = "eye-queue-depth-high"
  }
}

# Messages stuck alarm
resource "aws_cloudwatch_metric_alarm" "oldest_message_age" {
  alarm_name          = "eye-oldest-message-age"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateAgeOfOldestMessage"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 3600 # 1 hour
  alarm_description   = "Alert when messages are stuck in queue for too long"
  treat_missing_data  = "notBreaching"

  dimensions = {
    QueueName = aws_sqs_queue.video_queue.name
  }

  alarm_actions = [aws_sns_topic.alerts.arn]

  tags = {
    Name = "eye-oldest-message-age"
  }
}
