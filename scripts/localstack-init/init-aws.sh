#!/bin/bash

set -e

echo "Creating S3 buckets..."
awslocal s3 mb s3://hls-raw-ingest-dev
awslocal s3 mb s3://hls-processed-dev

echo "Creating SQS queue..."
awslocal sqs create-queue --queue-name hls-video-queue

echo "Verifying resources..."
awslocal s3 ls
awslocal sqs list-queues

echo "LocalStack initialization complete."
