#!/bin/bash

if [ -f .env ]; then
    set -a
    source .env
    set +a
fi

# Fallback values
DEFAULT_URL="https://api.video.miller.today"
BASE_URL="${1:-${API_ENDPOINT:-$DEFAULT_URL}}"
VIDEO_FILE="./test_assets/tempest_input.mp4"
USERNAME="${API_USERNAME:-admin}"
PASSWORD="${API_PASSWORD:-}"

if [ -z "$PASSWORD" ]; then
    echo "Error: API_PASSWORD environment variable is not set."
    echo "Set it via: export API_PASSWORD='your-password'"
    echo "Or create a .env file with API_PASSWORD=your-password"
    exit 1
fi

echo "Targeting: $BASE_URL"

if [ ! -f "$VIDEO_FILE" ]; then
    echo "Error: Test video not found at $VIDEO_FILE"
    exit 1
fi

# Login
echo "Authenticating..."
LOGIN_RESPONSE=$(curl -s -X POST -u "$USERNAME:$PASSWORD" "$BASE_URL/login")
TOKEN=$(echo "$LOGIN_RESPONSE" | jq -r '.token')

if [ -z "$TOKEN" ] || [ "$TOKEN" == "null" ]; then
    echo "Authentication failed."
    echo "Response: $LOGIN_RESPONSE"
    exit 1
fi
echo "   Token acquired."

# Init Upload
echo "Initializing Upload..."
FILENAME=$(basename "$VIDEO_FILE")
CONTENT_TYPE="video/mp4"

INIT_RESPONSE=$(curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"filename\": \"$FILENAME\", \"contentType\": \"$CONTENT_TYPE\"}" \
  "$BASE_URL/upload/init")

UPLOAD_URL=$(echo "$INIT_RESPONSE" | jq -r '.uploadUrl')
VIDEO_ID=$(echo "$INIT_RESPONSE" | jq -r '.videoId')
KEY=$(echo "$INIT_RESPONSE" | jq -r '.key')

if [ "$UPLOAD_URL" == "null" ]; then
    echo "Init failed."
    echo "Response: $INIT_RESPONSE"
    exit 1
fi
echo "   Video ID: $VIDEO_ID"

# Direct S3 Upload
echo "Uploading to S3 (Direct)..."
HTTP_CODE=$(curl -s -S -w "%{http_code}" -o /dev/null -X PUT -T "$VIDEO_FILE" \
  -H "Content-Type: $CONTENT_TYPE" \
  "$UPLOAD_URL")

if [ "$HTTP_CODE" -ne 200 ]; then
    echo "Upload failed with HTTP $HTTP_CODE"
    exit 1
fi
echo "   Upload successful."

# Complete Upload
echo "Finalizing Job..."
COMPLETE_RESPONSE=$(curl -s -w "%{http_code}" -o response.json -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"videoId\": \"$VIDEO_ID\", \"key\": \"$KEY\", \"filename\": \"$FILENAME\"}" \
  "$BASE_URL/upload/complete")

HTTP_CODE=$(tail -n1 <<< "$COMPLETE_RESPONSE")

if [[ "$HTTP_CODE" -eq 202 ]]; then
    echo "Job Queued."
    cat response.json | jq .
else
    echo "Finalization failed (HTTP $HTTP_CODE)."
    cat response.json
fi

rm -f response.json
