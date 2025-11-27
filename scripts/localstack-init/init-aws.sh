#!/bin/bash

set -e

echo "Creating S3 buckets..."
awslocal s3 mb s3://eye-raw-ingest-dev
awslocal s3 mb s3://eye-processed-dev

echo "Creating SQS queue..."
awslocal sqs create-queue --queue-name eye-video-queue

echo "Verifying resources..."
awslocal s3 ls
awslocal sqs list-queues

echo "LocalStack initialization complete."

