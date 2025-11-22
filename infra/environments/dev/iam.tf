resource "aws_iam_user" "worker_user" {
  name = "eye-worker-app"
}

resource "aws_iam_policy" "worker_policy" {
  name        = "eye-worker-policy"
  description = "Allows worker to read raw, write processed, and consume SQS"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:PutObject"]
        Resource = "${aws_s3_bucket.raw_ingest.arn}/*"
      },
      {
        Effect   = "Allow"
        Action   = ["s3:PutObject", "s3:PutObjectAcl"]
        Resource = "${aws_s3_bucket.processed.arn}/*"
      },
      {
        Effect = "Allow"
        Action = [
          "sqs:ReceiveMessage",
          "sqs:SendMessage",
          "sqs:DeleteMessage",
          "sqs:GetQueueAttributes"
        ]
        Resource = aws_sqs_queue.video_queue.arn
      }
    ]
  })
}

resource "aws_iam_user_policy_attachment" "worker_attach" {
  user       = aws_iam_user.worker_user.name
  policy_arn = aws_iam_policy.worker_policy.arn
}

resource "aws_iam_access_key" "worker_key" {
  user = aws_iam_user.worker_user.name
}
