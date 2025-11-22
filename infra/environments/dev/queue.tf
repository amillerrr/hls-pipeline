resource "aws_sqs_queue" "video_queue" {
  name                      = "video-transcode-queue"
  delay_seconds             = 0
  max_message_size          = 2048
  message_retention_seconds = 86400 # 1 day
  receive_wait_time_seconds = 10

  # Max Processing Time (10 mins)
  visibility_timeout_seconds = 600
}
