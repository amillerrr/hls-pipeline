#!/bin/bash

echo "1. Logging in to get JWT..."
# Extract token using grep/sed (Simple parsing)
TOKEN=$(curl -s -X POST -d "username=admin&password=secret" http://localhost:8080/login | grep -o '"token": *"[^"]*"' | grep -o '"[^"]*"$' | tr -d '"')

if [ -z "$TOKEN" ]; then
  echo "Login failed!"
  exit 1
fi

echo "Got Token: ${TOKEN:0:10}..."

echo "2. Starting Attack (20 Uploads)..."
for i in {1..20}
do
   echo "Upload #$i..."
   # Upload the test video (silent output, just status code)
   curl -s -o /dev/null -w "%{http_code}\n" \
     -H "Authorization: Bearer $TOKEN" \
     -F "video=@./test_assets/tempest_input.mp4" \
     http://localhost:8080/upload &
     
   # Sleep slightly to prevent blowing up your laptop
   sleep 1
done

echo "Attack complete."
