#!/bin/bash

BASE_URL="${1:-http://localhost:8080}"
USERNAME="admin"
PASSWORD="secret"

echo "Getting Token..."
TOKEN=$(curl -s -X POST -d "username=$USERNAME&password=$PASSWORD" "$BASE_URL/login" | grep -o '"token": *"[^"]*"' | cut -d'"' -f4)

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
