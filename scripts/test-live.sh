#!/bin/bash
# Test script for Live Streaming
# Usage: ./scripts/test-live.sh [video-file]

set -e

VIDEO_FILE="${1:-}"
LIVE_API="${LIVE_API:-http://localhost:8081}"
STREAM_NAME="test-stream-$(date +%s)"

echo "=========================================="
echo "HLS Pipeline - Live Streaming Test"
echo "=========================================="
echo ""

# Check if live service is running
echo ">>> Checking Live Service"
if ! curl -s "$LIVE_API/health" > /dev/null 2>&1; then
    echo "Error: Live service not available at $LIVE_API"
    echo "Start it with: docker-compose up -d live mediamtx"
    exit 1
fi
echo "  Live service is running"
echo ""

# Create SRT endpoint
echo ">>> Creating SRT Endpoint"
RESPONSE=$(curl -s -X POST "$LIVE_API/api/v1/live/srt/endpoints" \
    -H "Content-Type: application/json" \
    -d "{
        \"name\": \"$STREAM_NAME\",
        \"enableLlhls\": true,
        \"presets\": [\"720p\", \"480p\"],
        \"autoStart\": true
    }")

STREAM_ID=$(echo $RESPONSE | jq -r '.streamId')
SRT_URL=$(echo $RESPONSE | jq -r '.srtUrl')
PORT=$(echo $RESPONSE | jq -r '.port')

if [ "$STREAM_ID" = "null" ] || [ -z "$STREAM_ID" ]; then
    echo "Error: Failed to create stream"
    echo "Response: $RESPONSE"
    exit 1
fi

echo "  Stream created"
echo "  Stream ID:   $STREAM_ID"
echo "  Stream Name: $STREAM_NAME"
echo "  SRT Port:    $PORT"
echo ""

# Show SRT URL
echo ">>> SRT Ingest URL"
echo ""
echo "  $SRT_URL"
echo ""

# If video file provided, start streaming
if [ -n "$VIDEO_FILE" ] && [ -f "$VIDEO_FILE" ]; then
    echo ">>> Starting FFmpeg Stream"
    echo "Streaming: $VIDEO_FILE"
    echo ""
    
    # Extract port from SRT URL
    SRT_PORT=$(echo "$SRT_URL" | grep -oE ':[0-9]+' | head -1 | tr -d ':')
    
    # Start FFmpeg in background
    ffmpeg -re -i "$VIDEO_FILE" \
        -c:v libx264 -preset veryfast -tune zerolatency \
        -b:v 2500k -maxrate 2800k -bufsize 5000k \
        -g 60 -keyint_min 60 -sc_threshold 0 \
        -c:a aac -b:a 128k \
        -f mpegts \
        "srt://localhost:${SRT_PORT}?mode=caller&latency=200" &
    
    FFMPEG_PID=$!
    echo "  FFmpeg started (PID: $FFMPEG_PID)"
    echo ""
    
    # Wait for stream to become active
    echo ">>> Waiting for Stream to Become Active"
    for i in {1..30}; do
        sleep 2
        
        STATUS=$(curl -s "$LIVE_API/api/v1/live/streams/$STREAM_ID" | jq -r '.state')
        
        if [ "$STATUS" = "active" ]; then
            echo "✓ Stream is active!"
            break
        fi
        
        echo "  Waiting... (status: $STATUS)"
    done
    
    # Get stream info
    echo ""
    echo ">>> Stream Information"
    curl -s "$LIVE_API/api/v1/live/streams/$STREAM_ID" | jq .
    
    echo ""
    echo "=========================================="
    echo "Stream is Live!"
    echo "=========================================="
    echo ""
    echo "Playback URL:"
    OUTPUT_URL=$(curl -s "$LIVE_API/api/v1/live/streams/$STREAM_ID" | jq -r '.outputUrl')
    echo "  $OUTPUT_URL"
    echo ""
    echo "Press Ctrl+C to stop streaming..."
    echo ""
    
    # Wait for user interrupt
    trap "kill $FFMPEG_PID 2>/dev/null; echo ''; echo 'Stopping stream...'" INT
    wait $FFMPEG_PID 2>/dev/null || true
    
else
    echo ">>> Manual Streaming"
    echo ""
    echo "No video file provided. To stream manually:"
    echo ""
    echo "FFmpeg:"
    echo "  ffmpeg -re -i input.mp4 \\"
    echo "    -c:v libx264 -preset veryfast -tune zerolatency \\"
    echo "    -b:v 2500k -g 60 -keyint_min 60 \\"
    echo "    -c:a aac -b:a 128k \\"
    echo "    -f mpegts \\"
    echo "    \"srt://localhost:${PORT}?mode=caller&latency=200\""
    echo ""
    echo "OBS Studio:"
    echo "  Settings → Stream → Service: Custom"
    echo "  Server: srt://localhost:${PORT}?mode=caller&latency=200"
    echo ""
fi

# Cleanup function
cleanup() {
    echo ""
    echo ">>> Stopping Stream"
    curl -s -X POST "$LIVE_API/api/v1/live/streams/$STREAM_ID/stop" > /dev/null 2>&1 || true
    echo "  Stream stopped"
    
    echo ""
    echo ">>> Deleting Stream"
    curl -s -X DELETE "$LIVE_API/api/v1/live/streams/$STREAM_ID" > /dev/null 2>&1 || true
    echo "  Stream deleted"
}

# Register cleanup on exit if we started streaming
if [ -n "$FFMPEG_PID" ]; then
    trap cleanup EXIT
fi

echo ""
echo "=========================================="
echo "Test Complete"
echo "=========================================="
echo ""
echo "Useful commands:"
echo "  # View stream status"
echo "  curl $LIVE_API/api/v1/live/streams/$STREAM_ID"
echo ""
echo "  # Stop stream"
echo "  curl -X POST $LIVE_API/api/v1/live/streams/$STREAM_ID/stop"
echo ""
echo "  # Delete stream"
echo "  curl -X DELETE $LIVE_API/api/v1/live/streams/$STREAM_ID"
echo ""
echo "  # View all streams"
echo "  curl $LIVE_API/api/v1/live/streams"
echo ""

