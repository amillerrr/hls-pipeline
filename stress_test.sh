#!/bin/bash

if [ -f .env ]; then
    echo "Loading configuration from .env..."
    set -a
    source .env
    set +a
fi

BASE_URL="${1:-${API_ENDPOINT:-http://localhost:8080}}"
VIDEO_FILE="./test_assets/tempest_input.mp4"

echo "Targeting: $BASE_URL"

if [ ! -f "$VIDEO_FILE" ]; then
    echo "Error: Test video not found at $VIDEO_FILE"
    exit 1
fi

echo "1. Logging in to get JWT..."
TOKEN=$(curl -s -X POST -d "username=admin&password=secret" "$BASE_URL/login" | grep -o '"token": *"[^"]*"' | grep -o '"[^"]*"$' | tr -d '"')

if [ -z "$TOKEN" ]; then
  echo "Login failed! Could not reach API or credentials invalid."
  exit 1
fi

echo "Got Token: ${TOKEN:0:10}..."

echo "2. Starting Attack (10 Concurrent Uploads)..."
for i in {1..10}
do
   echo "Starting Upload #$i..."
   curl -s -o /dev/null -w "Job #$i: %{http_code}\n" \
     -H "Authorization: Bearer $TOKEN" \
     -F "video=@$VIDEO_FILE" \
     "$BASE_URL/upload" &
   sleep 0.5
done

wait
echo "Attack complete."
