# HLS Video Pipeline

A scalable video transcoding pipeline that converts uploaded videos to HLS (HTTP Live Streaming) format with multiple quality levels.

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Client    │────▶│   API       │────▶│   S3 Raw    │
│             │     │   Service   │     │   Bucket    │
└─────────────┘     └──────┬──────┘     └─────────────┘
                           │                    │
                           ▼                    │
                    ┌─────────────┐             │
                    │   SQS       │◀────────────┘
                    │   Queue     │
                    └──────┬──────┘
                           │
                           ▼
                    ┌─────────────┐     ┌─────────────┐
                    │   Worker    │────▶│ S3 Processed│
                    │   Service   │     │   Bucket    │
                    └──────┬──────┘     └──────┬──────┘
                           │                    │
                           ▼                    ▼
                    ┌─────────────┐     ┌─────────────┐
                    │  DynamoDB   │     │ CloudFront  │
                    │             │     │   CDN       │
                    └─────────────┘     └─────────────┘
```

## Project Structure

```
hls-pipeline/
├── cmd/
│   ├── api/main.go          # API service entry point (~50 lines)
│   └── worker/main.go       # Worker service entry point (~50 lines)
├── internal/
│   ├── config/              # Centralized configuration management
│   │   ├── config.go
│   │   └── config_test.go
│   ├── api/                 # HTTP server, handlers, middleware
│   │   ├── server.go
│   │   ├── handlers.go
│   │   ├── handlers_test.go
│   │   └── middleware.go
│   ├── worker/              # SQS polling, job processing
│   │   ├── worker.go
│   │   ├── downloader.go
│   │   └── uploader.go
│   ├── transcoder/          # FFmpeg, presets, playlist generation
│   │   ├── ffmpeg.go
│   │   ├── presets.go
│   │   ├── playlist.go
│   │   └── transcoder_test.go
│   ├── storage/             # S3 and DynamoDB clients
│   │   ├── s3.go
│   │   └── dynamodb.go
│   ├── auth/                # JWT and rate limiting
│   │   ├── jwt.go
│   │   ├── ratelimit.go
│   │   └── auth_test.go
│   ├── health/              # Health check functionality
│   │   ├── checker.go
│   │   └── checker_test.go
│   ├── metrics/             # Prometheus metrics
│   │   └── metrics.go
│   └── observability/       # OpenTelemetry tracing
│       └── tracer.go
├── pkg/models/              # Shared data types
│   ├── video.go
│   └── errors.go
├── infra/                   # Terraform infrastructure
│   └── ecr.tf
├── Makefile
├── go.mod
└── README.md
```

## Configuration

All configuration is centralized in `internal/config/config.go`. Environment variables:

### Required for API

| Variable | Description |
|----------|-------------|
| `S3_BUCKET` | Raw video upload bucket |
| `SQS_QUEUE_URL` | Processing queue URL |
| `DYNAMODB_TABLE` | Video metadata table |
| `JWT_SECRET` | Secret for JWT signing (min 32 chars in production) |

### Required for Worker

| Variable | Description |
|----------|-------------|
| `S3_BUCKET` | Raw video upload bucket |
| `PROCESSED_BUCKET` | HLS output bucket |
| `SQS_QUEUE_URL` | Processing queue URL |
| `DYNAMODB_TABLE` | Video metadata table |
| `CDN_DOMAIN` | CloudFront domain for playback URLs |

### Optional

| Variable | Default | Description |
|----------|---------|-------------|
| `ENV` | `dev` | Environment (dev/prod/production) |
| `PORT` | `8080` | API server port |
| `METRICS_PORT` | `2112` | Prometheus metrics port |
| `AWS_REGION` | `us-west-2` | AWS region |
| `MAX_CONCURRENT_JOBS` | `1` | Worker concurrency |
| `CORS_ALLOWED_ORIGINS` | (hardcoded) | Comma-separated origins |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OpenTelemetry endpoint |

## API Endpoints

### Public

- `GET /health` - Basic health check
- `GET /health/deep` - Deep health check (rate limited)
- `POST /login` - Authenticate and get JWT token
- `GET /latest` - Get most recently processed video

### Protected (requires JWT)

- `POST /upload/init` - Get presigned URL for upload
- `POST /upload/complete` - Confirm upload and queue processing

## Development

```bash
# Install dependencies
make deps

# Run tests
make test

# Run with coverage
make test-coverage

# Build binaries
make build

# Run locally
make run-api     # In one terminal
make run-worker  # In another terminal

# Lint code
make lint
```

## Quality Presets

Videos are transcoded to three quality levels:

| Preset | Resolution | Video Bitrate | Audio Bitrate |
|--------|------------|---------------|---------------|
| 1080p  | 1920x1080  | 5 Mbps        | 192 kbps      |
| 720p   | 1280x720   | 2.5 Mbps      | 128 kbps      |
| 480p   | 854x480    | 1 Mbps        | 96 kbps       |

## Metrics

Prometheus metrics are exposed at `/metrics` (internal network only):

### Worker Metrics
- `hls_videos_processed_total{status}` - Videos processed by status
- `hls_video_processing_duration_seconds` - Processing duration
- `hls_video_download_duration_seconds` - S3 download duration
- `hls_video_upload_duration_seconds` - S3 upload duration
- `hls_video_transcode_duration_seconds` - FFmpeg transcoding duration
- `hls_video_quality_score` - SSIM quality metric
- `hls_active_jobs` - Currently processing jobs

### API Metrics
- `hls_api_http_requests_total{method,path,status}` - HTTP requests
- `hls_api_http_request_duration_seconds` - Request duration
- `hls_api_auth_failures_total{reason}` - Auth failures
- `hls_api_uploads_initiated_total` - Upload initiations
- `hls_api_uploads_completed_total` - Completed uploads

## Security Features

- JWT authentication with configurable expiration
- Rate limiting on failed auth attempts
- Path traversal prevention on S3 keys
- CORS with configurable allowed origins
- Metrics endpoint restricted to internal networks
- Production mode enforces strong secrets

## License

MIT
