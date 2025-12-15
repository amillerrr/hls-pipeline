#!/bin/bash

BASE_URL="${1:-${API_ENDPOINT:-http://localhost:8080}}"
USERNAME="${API_USERNAME:-admin}"
PASSWORD="${API_PASSWORD:-}"

if [ -z "$PASSWORD" ]; then
    echo "Error: API_PASSWORD environment variable is not set."
    echo "For local development, set: export API_PASSWORD='secret'"
    exit 1
fi

echo "Getting Token from $BASE_URL..."
TOKEN=$(curl -s -X POST -u "$USERNAME:$PASSWORD" "$BASE_URL/login" | jq -r '.token')

if [ -z "$TOKEN" ] || [ "$TOKEN" == "null" ]; then
    echo "Authentication failed. Check credentials."
    exit 1
fi

echo "Starting Stress Test on Init Endpoint (Control Plane)..."
for i in {1..20}; do
   (
     curl -s -o /dev/null -w "Req $i: %{http_code}\n" \
     -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"filename": "stress_test.mp4", "contentType": "video/mp4"}' \
     "$BASE_URL/upload/init"
   ) &
done
wait
echo "Done."
