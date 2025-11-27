resource "aws_sqs_queue" "video_dlq" {
  name                      = "video-transcode-dlq"
  message_retention_seconds = 1209600 # 14 days
}

resource "aws_sqs_queue" "video_queue" {
  name                      = "video-transcode-queue"
  delay_seconds             = 0
  max_message_size          = 2048
  message_retention_seconds = 86400 # 1 day
  receive_wait_time_seconds = 10
  visibility_timeout_seconds = 960

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.video_dlq.arn
    maxReceiveCount     = 3 
  })
}

resource "aws_cloudwatch_metric_alarm" "dlq_depth" {
  alarm_name          = "eye-dlq-depth-alarm"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "1"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = "300"
  statistic           = "Sum"
  threshold           = "0"
  alarm_description   = "This metric monitors the DLQ for failed video jobs"
  dimensions = {
    QueueName = aws_sqs_queue.video_dlq.name
  }
}
