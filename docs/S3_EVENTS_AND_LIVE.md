# S3 Event-Driven Architecture & Live Streaming

This document covers the new features added to the HLS Pipeline:
1. **S3 Event Notifications** - Removing the API from the critical path
2. **Live Ingest** - SRT/RTMP support with MediaMTX sidecar

---

## S3 Event-Driven Architecture

### Problem Statement

Previously, the upload flow required:
1. Client requests pre-signed URL from API
2. Client uploads to S3
3. **Client calls `/complete` API endpoint**
4. API sends message to SQS
5. Worker processes the job

The issue: If the client crashes between steps 2 and 3, the file sits in S3 orphaned and no job is triggered.

### Solution: S3 Event Notifications

Now the flow is:
1. Client requests pre-signed URL from API
2. Client uploads to S3
3. **S3 automatically sends event to SQS**
4. Worker processes the job

No `/complete` call needed. Processing starts automatically when the upload finishes.

### Configuration

#### Terraform Setup

```hcl
# Enable S3 event notifications to SQS
module "s3_events" {
  source = "./modules/s3-events"

  bucket_name   = aws_s3_bucket.raw.id
  bucket_arn    = aws_s3_bucket.raw.arn
  queue_arn     = aws_sqs_queue.jobs.arn
  queue_url     = aws_sqs_queue.jobs.url
  
  # Only trigger on uploads in the uploads/ prefix
  filter_prefix = "uploads/"
  
  # Optionally filter by file extension
  # filter_suffix = ".mp4"
  
  events = ["s3:ObjectCreated:*"]
}
```

#### LocalStack Setup (Development)

```bash
aws --endpoint-url=http://localhost:4566 s3api put-bucket-notification-configuration \
  --bucket hls-pipeline-raw \
  --notification-configuration '{
    "QueueConfigurations": [{
      "QueueArn": "arn:aws:sqs:us-west-2:000000000000:hls-pipeline-jobs",
      "Events": ["s3:ObjectCreated:*"],
      "Filter": {
        "Key": {
          "FilterRules": [{
            "Name": "prefix",
            "Value": "uploads/"
          }]
        }
      }
    }]
  }'
```

### S3 Event Message Format

When a file is uploaded, S3 sends a message like this:

```json
{
  "Records": [
    {
      "eventVersion": "2.1",
      "eventSource": "aws:s3",
      "awsRegion": "us-west-2",
      "eventTime": "2024-01-15T10:30:00.000Z",
      "eventName": "ObjectCreated:Put",
      "s3": {
        "bucket": {
          "name": "hls-pipeline-raw",
          "arn": "arn:aws:s3:::hls-pipeline-raw"
        },
        "object": {
          "key": "uploads/abc123/video.mp4",
          "size": 104857600,
          "eTag": "d41d8cd98f00b204e9800998ecf8427e"
        }
      }
    }
  ]
}
```

### Worker Processing

The worker (`internal/worker/s3events.go`) handles both:
- **S3 Events** - Automatic triggers from uploads
- **API Messages** - Direct job submissions (backwards compatible)

```go
// ParseSQSMessage handles both message types
job, err := ParseSQSMessage(messageBody)
if err != nil {
    // Handle error
}

// job.Source will be either "s3_event" or "api"
switch job.Source {
case JobSourceS3Event:
    // Triggered by S3 upload
case JobSourceAPI:
    // Triggered by API call
}
```

### Filtering

The `S3EventFilter` controls which uploads trigger processing:

```go
filter := &S3EventFilter{
    AllowedPrefixes:   []string{"uploads/", "raw/"},
    DeniedPrefixes:    []string{"processed/", "temp/"},
    AllowedExtensions: []string{".mp4", ".mov", ".mkv"},
    MinSize:           1024,              // 1KB minimum
    MaxSize:           50 * 1024 * 1024 * 1024, // 50GB maximum
}
```

---

## Live Streaming

### Architecture

```
┌─────────────┐     ┌───────────────┐     ┌─────────────┐     ┌─────────┐
│   Encoder   │────▶│   MediaMTX    │────▶│    Live     │────▶│   S3    │
│  (OBS/etc)  │ SRT │   Sidecar     │RTSP │   Service   │     │ Output  │
└─────────────┘     └───────────────┘     └─────────────┘     └────┬────┘
                                                                   │
                                                                   ▼
                                                              ┌─────────┐
                                                              │   CDN   │
                                                              └─────────┘
```

### Components

1. **MediaMTX Sidecar** - Protocol conversion (SRT → RTSP, RTMP → RTSP)
2. **Live Service** - Stream management and CMAF packaging
3. **Live Packager** - Real-time FFmpeg encoding and segmentation

### SRT Ingest

SRT (Secure Reliable Transport) is recommended for live contribution:
- Reliable delivery over UDP
- Low latency
- Encryption support
- Handles packet loss gracefully

#### Create SRT Endpoint

```bash
curl -X POST http://localhost:8081/api/v1/live/srt/endpoints \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-live-stream",
    "enableLlhls": true,
    "presets": ["1080p", "720p", "480p"],
    "dvrWindowSize": 30,
    "autoStart": true
  }'
```

Response:
```json
{
  "streamId": "a1b2c3d4",
  "streamName": "my-live-stream",
  "port": 9000,
  "srtUrl": "srt://0.0.0.0:9000?mode=caller&latency=200&streamid=a1b2c3d4",
  "status": "ready"
}
```

#### Stream with FFmpeg

```bash
ffmpeg -re -i input.mp4 \
  -c:v libx264 -preset veryfast -tune zerolatency \
  -b:v 5000k -maxrate 5500k -bufsize 10000k \
  -g 60 -keyint_min 60 -sc_threshold 0 \
  -c:a aac -b:a 128k \
  -f mpegts \
  "srt://localhost:9000?mode=caller&latency=200"
```

#### Stream with OBS

1. Settings → Stream → Service: Custom
2. Server: `srt://your-host:9000?mode=caller&latency=200`
3. Stream Key: (leave empty or use streamId)

### RTMP Ingest

For compatibility with existing encoder workflows:

```bash
# Create RTMP stream
curl -X POST http://localhost:8081/api/v1/live/streams \
  -H "Content-Type: application/json" \
  -d '{
    "name": "rtmp-stream",
    "inputType": "rtmp",
    "inputPath": "/live/mystream"
  }'

# Stream with FFmpeg
ffmpeg -re -i input.mp4 -c copy -f flv rtmp://localhost:1935/live/mystream
```

### Live Packager

The live packager (`internal/live/packager.go`) performs real-time processing:

1. **Receives stream** from MediaMTX via RTSP
2. **Transcodes** to multiple bitrates with FFmpeg
3. **Packages** into HLS/CMAF segments
4. **Uploads** segments to S3 in real-time
5. **Updates** manifests continuously

```go
packager, err := NewLivePackager(&LivePackagerConfig{
    StreamID:        "abc123",
    InputURL:        "rtsp://mediamtx:8554/live/abc123",
    OutputBucket:    "hls-pipeline-processed",
    OutputPrefix:    "live/abc123",
    SegmentDuration: 4.0,
    PartDuration:    0.5,  // For LL-HLS
    Presets:         []string{"1080p", "720p", "480p"},
    EnableLLHLS:     true,
}, logger)

err = packager.Start(ctx)
```

### Stream Lifecycle

```
                    ┌───────────┐
                    │   IDLE    │
                    └─────┬─────┘
                          │ CreateStream()
                          ▼
                    ┌───────────┐
       ┌───────────▶│  STARTING │
       │            └─────┬─────┘
       │                  │ Input connected
       │                  ▼
       │            ┌───────────┐
       │            │  ACTIVE   │◀─────┐
       │            └─────┬─────┘      │
       │                  │            │ Reconnect
       │                  │            │
       │     ┌────────────┼────────────┤
       │     │            │            │
       │     ▼            ▼            │
       │ ┌───────┐  ┌──────────┐       │
       │ │ ERROR │  │ STOPPING │       │
       │ └───┬───┘  └────┬─────┘       │
       │     │           │             │
       │     │           ▼             │
       │     │     ┌───────────┐       │
       │     └────▶│  STOPPED  │───────┘
       │           └───────────┘
       │                 │
       └─────────────────┘
             StartStream()
```

### API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/v1/live/streams` | Create a new stream |
| `GET` | `/api/v1/live/streams` | List all streams |
| `GET` | `/api/v1/live/streams/{id}` | Get stream details |
| `POST` | `/api/v1/live/streams/{id}/start` | Start a stream |
| `POST` | `/api/v1/live/streams/{id}/stop` | Stop a stream |
| `DELETE` | `/api/v1/live/streams/{id}` | Delete a stream |
| `POST` | `/api/v1/live/srt/endpoints` | Create SRT endpoint |
| `GET` | `/api/v1/live/srt/endpoints/{id}` | Get SRT endpoint |

### Metrics

```prometheus
# Active streams
live_streams_active

# Stream duration histogram
live_stream_duration_seconds_bucket{input_type="srt"}

# Ingest statistics
live_ingest_bitrate_bps{stream_id="abc123"}
live_ingest_frames_received_total{stream_id="abc123"}
live_ingest_frames_dropped_total{stream_id="abc123"}

# Packaging latency
live_packaging_latency_seconds{stream_id="abc123"}

# Segment creation
live_segments_created_total{stream_id="abc123",variant="1080p"}

# SRT-specific
srt_latency_ms{stream_id="abc123"}
srt_packets_lost_total{stream_id="abc123"}
```

### Infrastructure (Terraform)

```hcl
module "live" {
  source = "./modules/live"

  environment       = "prod"
  vpc_id            = var.vpc_id
  subnet_ids        = var.subnet_ids
  cluster_id        = aws_ecs_cluster.main.id
  live_image        = "your-registry/hls-live:latest"
  output_bucket     = aws_s3_bucket.processed.id
  cdn_domain        = aws_cloudfront_distribution.main.domain_name

  # Resources
  cpu           = 2048
  memory        = 4096
  desired_count = 2

  # Ports
  srt_port_range_start = 9000
  srt_port_range_end   = 9100
  rtmp_port            = 1935

  # Enable NLB for SRT/RTMP traffic
  enable_nlb = true
}
```

### Docker Compose (Development)

```yaml
services:
  mediamtx:
    image: bluenviron/mediamtx:latest
    ports:
      - "1935:1935"   # RTMP
      - "8554:8554"   # RTSP
      - "8890:8890"   # SRT
      - "9997:9997"   # API

  live:
    build:
      context: .
      dockerfile: Dockerfile.live
    ports:
      - "8081:8080"
      - "9000-9010:9000-9010/udp"
    environment:
      - MEDIAMTX_URL=mediamtx
      - OUTPUT_BUCKET=hls-pipeline-processed
    depends_on:
      - mediamtx
```

---

## Migration Guide

### Updating Existing Deployments

1. **Deploy S3 Event Notifications**
   ```bash
   cd infra/environments/prod
   terraform apply -target=module.s3_events
   ```

2. **Update Worker** - The new worker handles both S3 events and API messages
   ```bash
   # Deploy updated worker
   aws ecs update-service --cluster hls-pipeline --service worker --force-new-deployment
   ```

3. **Deprecate `/complete` Endpoint** - The API's `/complete` endpoint is now optional
   - Existing clients can continue using it (backwards compatible)
   - New clients should just upload to S3

4. **Add Live Infrastructure** (optional)
   ```bash
   terraform apply -target=module.live
   ```

### Backwards Compatibility

- The worker accepts both S3 events and direct API messages
- Existing `/complete` API endpoint still works
- No client changes required (but recommended for simplicity)

