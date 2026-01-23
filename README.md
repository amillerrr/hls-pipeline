# HLS Video Pipeline

A scalable, cloud-native video transcoding pipeline built with Go, supporting HLS, LL-HLS, CMAF, Multi-CDN, DRM, and Server-Side Ad Insertion (SSAI).

## Architecture Overview

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Client    │────▶│   API       │────▶│  S3 Raw     │
│             │     │  Service    │     │  Bucket     │
└─────────────┘     └──────┬──────┘     └──────┬──────┘
                          │                    │
                          ▼                    │
                   ┌─────────────┐             │
                   │    SQS      │◀────────────┘
                   │   Queue     │
                   └──────┬──────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────┐
│                    Worker Service                        │
│  ┌───────────┐  ┌───────────┐  ┌───────────┐           │
│  │ Transcode │─▶│  Package  │─▶│  Encrypt  │           │
│  │  (FFmpeg) │  │(Shaka/FFM)│  │   (DRM)   │           │
│  └───────────┘  └───────────┘  └───────────┘           │
└──────────────────────────┬──────────────────────────────┘
                          │
                          ▼
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│S3 Processed │────▶│  CloudFront │────▶│   Player    │
│   Bucket    │     │  / Multi-CDN│     │             │
└─────────────┘     └─────────────┘     └─────────────┘
```

## Features

### Core Video Processing
- **Multi-bitrate transcoding** with customizable encoding ladder
- **HLS v9 / LL-HLS support** with partial segments (EXT-X-PART)
- **CMAF packaging** for unified streaming
- **DASH manifest generation**
- **I-frame playlists** for trick play support

### Content Protection (DRM)
- **Widevine** - CENC encryption with PSSH generation
- **FairPlay** - CBCS encryption with HLS signaling
- **PlayReady** - CENC encryption with PRO header generation
- **CPIX key exchange** for enterprise key management
- **Multi-DRM** - Support all systems simultaneously

### CDN & Delivery
- **Multi-CDN routing** with weighted, latency, or geo-based selection
- **CDN health monitoring** with automatic failover
- **Session stickiness** for consistent playback
- **Manifest rewriting** for CDN-specific URLs

### Ad Insertion (SSAI)
- **SCTE-35 marker handling** - Parse, generate, and preserve markers
- **AWS MediaTailor integration** - Personalized ad insertion
- **Ad break management** - Cue-out/cue-in support
- **Tracking event reporting**

### Infrastructure
- **AWS-native** - S3, SQS, DynamoDB, ECS, CloudFront
- **Terraform IaC** - Complete infrastructure as code
- **OpenTelemetry** - Distributed tracing
- **Prometheus metrics** - Comprehensive observability

## Quick Start

### Prerequisites

- Go 1.22+
- Docker
- AWS CLI configured
- Terraform 1.5+
- FFmpeg 6.0+ (with libx264, libfdk-aac)
- Shaka Packager (optional, for advanced packaging)

### Local Development

```bash
# Clone the repository
git clone https://github.com/amillerrr/hls-pipeline.git
cd hls-pipeline

# Install dependencies
go mod download

# Copy environment template
cp .env.example .env
# Edit .env with your configuration

# Run the API locally
go run cmd/api/main.go

# Run the worker locally  
go run cmd/worker/main.go
```

### Docker

```bash
# Build images
docker build -t hls-pipeline-api -f Dockerfile --target api .
docker build -t hls-pipeline-worker -f Dockerfile --target worker .

# Run with docker-compose
docker-compose up
```

### Deploy to AWS

```bash
cd infra/environments/dev

# Initialize Terraform
terraform init

# Review the plan
terraform plan

# Apply
terraform apply
```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `AWS_REGION` | AWS region | `us-west-2` |
| `S3_BUCKET` | Raw uploads bucket | Required |
| `PROCESSED_BUCKET` | Processed output bucket | Required |
| `SQS_QUEUE_URL` | Job queue URL | Required |
| `DYNAMODB_TABLE` | Video metadata table | Required |
| `CDN_DOMAIN` | CloudFront domain | Required |
| `JWT_SECRET` | JWT signing secret | Required |
| `ENABLE_LLHLS` | Enable LL-HLS | `true` |
| `ENABLE_CMAF` | Enable CMAF packaging | `true` |
| `ENABLE_DRM` | Enable DRM encryption | `false` |
| `ENABLE_SSAI` | Enable ad insertion | `false` |
| `ENABLE_MULTI_CDN` | Enable multi-CDN | `false` |

### Presets Configuration

Encoding presets are configured in `configs/presets.yaml`:

```yaml
collections:
  - name: default
    presets:
      - name: "1080p"
        width: 1920
        height: 1080
        videoBitrate: "5000k"
        audioBitrate: "192k"
        profile: "high"
        level: "4.2"
```

## API Reference

### Upload Video

```bash
POST /api/v1/videos/upload
Content-Type: application/json

{
  "filename": "video.mp4",
  "contentType": "video/mp4",
  "fileSize": 104857600,
  "options": {
    "enableLlhls": true,
    "enableDrm": false,
    "presets": ["1080p", "720p", "480p"]
  }
}
```

### Get Video Status

```bash
GET /api/v1/videos/{videoId}
```

### List Videos

```bash
GET /api/v1/videos?limit=20&nextToken=xxx
```

## Project Structure

```
hls-pipeline/
├── cmd/
│   ├── api/           # API service entrypoint
│   └── worker/        # Worker service entrypoint
├── internal/
│   ├── api/           # HTTP handlers and middleware
│   ├── config/        # Configuration management
│   ├── transcoder/    # FFmpeg transcoding
│   ├── packager/      # CMAF/HLS packaging
│   ├── cdn/           # Multi-CDN routing
│   ├── drm/           # DRM encryption
│   ├── ssai/          # Ad insertion
│   ├── metrics/       # Prometheus metrics
│   └── worker/        # Job processing
├── pkg/
│   ├── models/        # Data models
│   └── hlsutils/      # HLS parsing utilities
├── configs/           # Configuration files
│   └── presets.yaml   # Encoding presets
├── infra/
│   ├── modules/       # Terraform modules
│   └── environments/  # Environment configs
└── scripts/           # Utility scripts
```

## Monitoring

### Prometheus Metrics

Key metrics exposed at `/metrics`:

- `hls_pipeline_transcode_duration_seconds` - Transcoding duration
- `hls_pipeline_transcode_jobs_total` - Total jobs by status
- `hls_pipeline_cdn_health_status` - CDN provider health
- `hls_pipeline_drm_encryption_duration_seconds` - DRM encryption time
- `hls_pipeline_ssai_ad_breaks_total` - Ad breaks served

### Health Checks

- `GET /health` - Liveness check
- `GET /ready` - Readiness check (includes dependencies)

## Testing

```bash
# Run unit tests
go test ./...

# Run with coverage
go test -cover ./...

# Run integration tests
go test -tags=integration ./...

# Run benchmarks
go test -bench=. ./internal/transcoder/
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request
