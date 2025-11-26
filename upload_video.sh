#!/bin/bash

if [ -f .env ]; then
    echo "Loading configuration from .env..."
    set -a
    source .env
    set +a
fi

DEFAULT_URL="https://api.toptal.miller.today"
BASE_URL="${1:-${API_ENDPOINT:-$DEFAULT_URL}}"
VIDEO_FILE="./test_assets/tempest_input.mp4"
USERNAME="admin"
PASSWORD="secret"

echo "Targeting: $BASE_URL"

if [ ! -f "$VIDEO_FILE" ]; then
    echo "Error: Test video not found at $VIDEO_FILE"
    exit 1
fi

echo "Authenticating"
set +e
LOGIN_RESPONSE=$(curl -s -S -L -k -X POST -d "username=$USERNAME&password=$PASSWORD" "$BASE_URL/login" 2>&1)
CURL_EXIT=$?
set -e

if [ $CURL_EXIT -ne 0 ]; then
    echo "FAILED."
    echo "Critical Error: Could not connect to API at $BASE_URL"
    echo "Curl Output: $LOGIN_RESPONSE"
    exit 1
fi

TOKEN=$(echo "$LOGIN_RESPONSE" | grep -o '"token": *"[^"]*"' | grep -o '"[^"]*"$' | tr -d '"')

if [ -z "$TOKEN" ]; then
    echo "FAILED."
    echo "Error: Authentication failed."
    echo "Server Response: $LOGIN_RESPONSE"
    exit 1
fi
echo "Success."

echo "Starting Upload..."
set +e
HTTP_CODE=$(curl -s -S -L -k -w "%{http_code}" -o response.json \
  -H "Authorization: Bearer $TOKEN" \
  -F "video=@$VIDEO_FILE" \
  "$BASE_URL/upload")
CURL_EXIT=$?
set -e

if [ $CURL_EXIT -ne 0 ]; then
    echo "FAILED."
    echo "Error: Network failure during upload."
    rm -f response.json
    exit 1
fi

if [[ "$HTTP_CODE" -eq 202 ]]; then
    echo "Success (HTTP 202)."
    echo "Job ID: $(cat response.json | grep -o '"id": *"[^"]*"' | cut -d'"' -f4)"
    echo "Full Response:"
    cat response.json
    echo ""
else
    echo "FAILED (HTTP $HTTP_CODE)."
    cat response.json
    echo ""
    rm -f response.json
    exit 1
fi

rm -f response.json
echo "Done."

