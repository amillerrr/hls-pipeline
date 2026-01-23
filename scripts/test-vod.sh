#!/bin/bash
# Test script for VOD upload with S3 event-driven processing
# Usage: ./scripts/test-vod.sh <video-file>

set -e

VIDEO_FILE="${1:-sample.mp4}"
API_URL="${API_URL:-http://localhost:8080}"
AWS_ENDPOINT="${AWS_ENDPOINT:-http://localhost:4566}"
RAW_BUCKET="${RAW_BUCKET:-hls-pipeline-raw}"
VIDEO_ID=$(uuidgen | cut -c1-8 | tr '[:upper:]' '[:lower:]')

echo "=========================================="
echo "HLS Pipeline - VOD Upload Test"
echo "=========================================="
echo "Video File: $VIDEO_FILE"
echo "Video ID:   $VIDEO_ID"
echo ""

# Check if file exists
if [ ! -f "$VIDEO_FILE" ]; then
    echo "Error: Video file not found: $VIDEO_FILE"
    echo "Usage: $0 <video-file>"
    exit 1
fi

FILE_SIZE=$(stat -f%z "$VIDEO_FILE" 2>/dev/null || stat -c%s "$VIDEO_FILE")
echo "File Size:  $FILE_SIZE bytes"
echo ""

# Method 1: Direct S3 upload (event-driven, no API needed for triggering)
echo ">>> Method 1: Direct S3 Upload (Event-Driven)"
echo "Uploading to S3..."

S3_KEY="uploads/${VIDEO_ID}/$(basename $VIDEO_FILE)"

aws --endpoint-url="$AWS_ENDPOINT" s3 cp "$VIDEO_FILE" "s3://${RAW_BUCKET}/${S3_KEY}"

echo "  Uploaded to s3://${RAW_BUCKET}/${S3_KEY}"
echo ""
echo "The S3 event notification will automatically trigger processing."
echo "No /complete API call needed!"
echo ""

# Wait and check for processing
echo ">>> Checking Processing Status"
echo "Waiting for worker to pick up the job..."

for i in {1..30}; do
    sleep 2
    
    # Check SQS queue (should be empty if worker processed it)
    MSG_COUNT=$(aws --endpoint-url="$AWS_ENDPOINT" sqs get-queue-attributes \
        --queue-url "http://localhost:4566/000000000000/hls-pipeline-jobs" \
        --attribute-names ApproximateNumberOfMessages \
        --query 'Attributes.ApproximateNumberOfMessages' \
        --output text 2>/dev/null || echo "0")
    
    if [ "$MSG_COUNT" = "0" ]; then
        echo "  Job picked up by worker"
        break
    fi
    
    echo "  Waiting... (messages in queue: $MSG_COUNT)"
done

echo ""

# Check for output
echo ">>> Checking Output"
echo "Looking for processed files..."

sleep 5  # Give worker time to process

OUTPUT_COUNT=$(aws --endpoint-url="$AWS_ENDPOINT" s3 ls "s3://hls-pipeline-processed/processed/${VIDEO_ID}/" 2>/dev/null | wc -l || echo "0")

if [ "$OUTPUT_COUNT" -gt 0 ]; then
    echo " Found $OUTPUT_COUNT output files:"
    aws --endpoint-url="$AWS_ENDPOINT" s3 ls "s3://hls-pipeline-processed/processed/${VIDEO_ID}/" | head -20
else
    echo "   Output not ready yet. Check worker logs:"
    echo "   docker-compose logs -f worker"
fi

echo ""
echo "=========================================="
echo "Test Complete"
echo "=========================================="
echo ""
echo "Playback URL (when ready):"
echo "  http://localhost:8080/processed/${VIDEO_ID}/master.m3u8"
echo ""
echo "To monitor processing:"
echo "  docker-compose logs -f worker"
echo ""

