# HLS Pipeline - Cloud-Native Video Processing Pipeline

A production-ready, serverless video transcoding pipeline that converts uploaded videos into adaptive bitrate HLS streams for seamless playback across devices and network conditions.

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [System Components](#system-components)
- [Data Flow](#data-flow)
- [Getting Started](#getting-started)
- [Execution Instructions](#execution-instructions)
- [Playback Testing](#playback-testing)
- [Technical Decisions & Trade-offs](#technical-decisions--trade-offs)
- [API Reference](#api-reference)
- [Observability](#observability)
- [CI/CD Pipeline](#cicd-pipeline)

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                              HLS PIPELINE ARCHITECTURE                              │
└─────────────────────────────────────────────────────────────────────────────────────┘

                                    ┌──────────────┐
                                    │   Browser    │
                                    │  (HLS.js)    │
                                    └──────┬───────┘
                                           │
                    ┌──────────────────────┼──────────────────────┐
                    │                      │                      │
                    ▼                      │                      ▼
           ┌───────────────┐               │             ┌───────────────┐
           │  CloudFront   │               │             │     Route53   │
           │     CDN       │◄──────────────┘             │      DNS      │
           │ (HLS Delivery)│                             └───────┬───────┘
           └───────┬───────┘                                     │
                   │                                             │
                   ▼                                             ▼
           ┌───────────────┐                             ┌───────────────┐
           │  S3 Processed │                             │      ALB      │
           │    Bucket     │                             │ (HTTPS/TLS)   │
           │  (HLS Files)  │                             └───────┬───────┘
           └───────────────┘                                     │
                   ▲                                             ▼
                   │                                     ┌───────────────┐
                   │                                     │   ECS Fargate │
                   │         ┌─────────────┐             │   ┌───────┐   │
                   │         │    SQS      │◄────────────┤   │  API  │   │
                   │         │   Queue     │             │   │Service│   │
                   │         └──────┬──────┘             │   └───────┘   │
                   │                │                    │       │       │
                   │                ▼                    │       ▼       │
                   │         ┌─────────────┐             │ ┌───────────┐ │
                   │         │ ECS Fargate │             │ │  Presign  │ │
                   │         │  ┌───────┐  │             │ │   URL     │ │
                   └─────────┤  │Worker │  │             │ └─────┬─────┘ │
                             │  │Service│  │             └───────┼───────┘
                             │  └───┬───┘  │                     │
                             │      │      │                     ▼
                             │   FFmpeg    │             ┌───────────────┐
                             │  Transcode  │◄────────────│   S3 Raw      │
                             └─────────────┘             │   Ingest      │
                                                         │   Bucket      │
                                                         └───────────────┘
                                                                 ▲
                                                                 │
                                                         Direct S3 Upload
                                                         (Presigned URL)
```

### High-Level Flow

```
┌────────┐    ┌─────┐    ┌────┐    ┌─────┐    ┌──────┐    ┌─────────┐    ┌────────┐
│ Client │───►│Login│───►│Init│───►│ S3  │───►│Queue │───►│Transcode│───►│Playback│
└────────┘    └─────┘    └────┘    └─────┘    └──────┘    └─────────┘    └────────┘
                │           │          │          │            │             │
                ▼           ▼          ▼          ▼            ▼             ▼
              JWT       Presigned   Direct     SQS Job      FFmpeg →      HLS.js
             Token        URL       Upload     Message     HLS Output     Player
```

---

## System Components

### API Service (`cmd/api`)

| Component | Purpose |
|-----------|---------|
| `/login` | Basic Auth → JWT token exchange |
| `/upload/init` | Generate S3 presigned URLs for direct upload |
| `/upload/complete` | Verify upload & queue transcoding job |
| `/latest` | Retrieve most recent processed video |
| `/health` | ALB health check endpoint |
| `/metrics` | Prometheus metrics (internal only) |

### Worker Service (`cmd/worker`)

| Component | Purpose |
|-----------|---------|
| SQS Poller | Long-poll queue for new jobs |
| S3 Downloader | Fetch raw video from ingest bucket |
| FFmpeg Transcoder | Multi-bitrate HLS encoding |
| S3 Uploader | Push HLS segments to processed bucket |
| Quality Metrics | SSIM calculation for quality assurance |

### Infrastructure (`infra/`)

| Resource | Purpose |
|----------|---------|
| VPC + Subnets | Network isolation |
| ECS Fargate | Serverless container orchestration |
| ALB | HTTPS termination, request routing |
| S3 (x2) | Raw uploads + processed HLS storage |
| SQS + DLQ | Job queue with dead-letter handling |
| CloudFront | Global CDN for HLS delivery |
| Route53 | DNS management |
| ACM | TLS certificates |
| CloudWatch | Logs, metrics, alarms |
| X-Ray | Distributed tracing |

---

## Data Flow

### Upload Flow (Detailed)

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                              UPLOAD SEQUENCE                                 │
└──────────────────────────────────────────────────────────────────────────────┘

  Client                API                  S3 Raw              SQS
    │                    │                     │                  │
    │  POST /login       │                     │                  │
    │  (Basic Auth)      │                     │                  │
    │───────────────────►│                     │                  │
    │                    │                     │                  │
    │◄───────────────────│                     │                  │
    │   { token: JWT }   │                     │                  │
    │                    │                     │                  │
    │  POST /upload/init │                     │                  │
    │  + Bearer Token    │                     │                  │
    │───────────────────►│                     │                  │
    │                    │  GeneratePresignURL │                  │
    │                    │────────────────────►│                  │
    │                    │◄────────────────────│                  │
    │◄───────────────────│                     │                  │
    │  { uploadUrl,      │                     │                  │
    │    videoId, key }  │                     │                  │
    │                    │                     │                  │
    │  PUT (presigned)   │                     │                  │
    │  [video bytes]     │                     │                  │
    │─────────────────────────────────────────►│                  │
    │◄─────────────────────────────────────────│                  │
    │        200 OK      │                     │                  │
    │                    │                     │                  │
    │ POST /upload/complete                    │                  │
    │───────────────────►│                     │                  │
    │                    │    HeadObject       │                  │
    │                    │────────────────────►│                  │
    │                    │◄────────────────────│                  │
    │                    │                     │   SendMessage    │
    │                    │───────────────────────────────────────►│
    │◄───────────────────│                     │                  │
    │  202 Accepted      │                     │                  │
    │  { status:         │                     │                  │
    │    "processing" }  │                     │                  │
```

### Processing Flow (Detailed)

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                            PROCESSING SEQUENCE                               │
└──────────────────────────────────────────────────────────────────────────────┘

   SQS              Worker            S3 Raw         FFmpeg        S3 Processed
    │                  │                 │              │               │
    │  ReceiveMessage  │                 │              │               │
    │◄─────────────────│                 │              │               │
    │                  │                 │              │               │
    │  { videoId,      │                 │              │               │
    │    s3Key,        │                 │              │               │
    │    bucket }      │                 │              │               │
    │─────────────────►│                 │              │               │
    │                  │                 │              │               │
    │                  │   GetObject     │              │               │
    │                  │────────────────►│              │               │
    │                  │◄────────────────│              │               │
    │                  │  [video bytes]  │              │               │
    │                  │                 │              │               │
    │                  │   Transcode to HLS             │               │
    │                  │───────────────────────────────►│               │
    │                  │   • 1080p (5 Mbps)             │               │
    │                  │   • 720p  (2.5 Mbps)           │               │
    │                  │   • 480p  (1 Mbps)             │               │
    │                  │◄───────────────────────────────│               │
    │                  │   [.m3u8 + .ts files]          │               │
    │                  │                 │              │               │
    │                  │   PutObject (batch)            │               │
    │                  │────────────────────────────────────────────────►
    │                  │◄────────────────────────────────────────────────
    │                  │                 │              │               │
    │  DeleteMessage   │                 │              │               │
    │◄─────────────────│                 │              │               │
```

---

## Getting Started

### Prerequisites

- AWS CLI configured with appropriate credentials
- Terraform >= 1.0
- Go 1.24+
- Docker
- A domain hosted in Route53

### Quick Start

```bash
# Clone the repository
git clone https://github.com/amillerrr/hls-pipeline.git
cd hls-pipeline

# Bootstrap Terraform state (one-time)
make bootstrap

# Deploy infrastructure
make deploy

# Build and push containers
make build push

# Force ECS deployment
make ecs-deploy
```

---

## Execution Instructions

### 1. Initial Setup

```bash
# Install dependencies
go mod download

# Verify Go version
go version  # Should be 1.24+
```

### 2. Deploy Infrastructure

```bash
# Initialize and apply Terraform
make deploy

# This will output:
# - API_ENDPOINT: https://api.video.miller.today
# - CDN_DOMAIN: video.miller.today
# - ECR repository URLs
```

### 3. Build & Deploy Services

```bash
# Build Docker images
make build

# Push to ECR
make push

# Deploy to ECS
make ecs-deploy
```

### 4. Upload a Video

```bash
# Place a test video in test_assets/
mkdir -p test_assets
cp /path/to/video.mp4 test_assets/tempest_input.mp4

# Run the upload script
./upload_video.sh
```

---

## Playback Testing

### Option 1: Demo Player (Recommended)

Open `demo.html` in a browser for a full-featured player with:
- Real-time quality level display
- Bitrate monitoring
- Buffer status
- Event logging for ABR switches

### Option 2: Debug Player

Open `index.html` for a minimal debug interface.

### Option 3: Direct HLS URL

After processing completes, retrieve the playback URL:

```bash
curl https://api.video.miller.today/latest
```

Response:
```json
{
  "videoId": "abc123",
  "playbackUrl": "https://video.miller.today/hls/abc123/master.m3u8",
  "processedAt": "2025-11-28T10:30:00Z"
}
```

### Testing ABR Switching

1. Open `demo.html` in Chrome
2. Open DevTools → Network tab
3. Select "Slow 3G" or "Fast 3G" throttling
4. Observe quality switching in the player UI
5. Watch `.ts` segment requests change resolution

---

## Technical Decisions & Trade-offs

### Why Presigned URLs for Upload?

| Approach | Pros | Cons |
|----------|------|------|
| **Presigned URL (chosen)** | Bypasses API for large files, scales infinitely | Extra round-trip for URL generation |
| Direct to API | Simpler client code | API becomes bottleneck, memory pressure |
| Multipart through API | Progress tracking | Complex implementation, still bottlenecked |

**Decision**: Presigned URLs allow direct S3 upload, eliminating API as a bottleneck for large video files.

### Why SQS over Direct Processing?

| Approach | Pros | Cons |
|----------|------|------|
| **SQS Queue (chosen)** | Decoupled, retry-able, scalable | Added latency (~seconds) |
| Synchronous | Immediate feedback | Blocks API, timeout risk |
| Lambda trigger | Event-driven | 15-min limit, cold starts |

**Decision**: SQS provides reliable delivery with automatic retries and dead-letter handling for failed jobs.

### Why Multi-Resolution HLS?

Adaptive bitrate streaming ensures:
- **1080p @ 5 Mbps**: High-quality for fast connections
- **720p @ 2.5 Mbps**: Balanced quality/bandwidth
- **480p @ 1 Mbps**: Mobile/poor connection fallback

This covers 95%+ of viewer scenarios without manual quality selection.

### Cost Optimization Strategies

| Strategy | Implementation | Savings |
|----------|----------------|---------|
| **Fargate Spot for workers** | `capacity_provider = FARGATE_SPOT` | Up to 70% compute cost |
| **S3 Intelligent Tiering** | Lifecycle rule after 30 days | Automatic cold storage |
| **Raw file deletion** | 1-day expiration on uploads | Eliminates storage waste |
| **CloudFront caching** | 1-hour default TTL | Reduces S3 GET requests |

**Trade-off**: Spot instances can be interrupted. Workers use graceful shutdown to complete in-progress jobs, but long videos may fail during interruption.

### What I Would Do Differently in Production

1. **Secrets Management**: Use AWS Secrets Manager with rotation for JWT secrets and API credentials
2. **Multi-region**: Deploy to multiple regions with Route53 latency-based routing
3. **GPU Transcoding**: Use EC2 G4dn instances for 10x faster encoding
4. **Chunked Upload**: Implement multipart upload for files > 5GB
5. **Progress Webhooks**: Add callback URLs for processing status updates
6. **Content Moderation**: Integrate AWS Rekognition for automated content screening

---

## API Reference

### Authentication

```http
POST /login
Authorization: Basic base64(username:password)

Response:
{
  "token": "eyJhbGciOiJIUzI1NiIs..."
}
```

### Initialize Upload

```http
POST /upload/init
Authorization: Bearer <token>
Content-Type: application/json

{
  "filename": "video.mp4",
  "contentType": "video/mp4"
}

Response:
{
  "uploadUrl": "https://s3.amazonaws.com/...",
  "videoId": "uuid-v4",
  "key": "uploads/uuid-v4.mp4"
}
```

### Complete Upload

```http
POST /upload/complete
Authorization: Bearer <token>
Content-Type: application/json

{
  "videoId": "uuid-v4",
  "key": "uploads/uuid-v4.mp4",
  "filename": "video.mp4"
}

Response (202):
{
  "videoId": "uuid-v4",
  "status": "processing",
  "message": "Video queued for processing"
}
```

### Get Latest Video

```http
GET /latest

Response:
{
  "videoId": "uuid-v4",
  "playbackUrl": "https://cdn.example.com/hls/uuid-v4/master.m3u8",
  "processedAt": "2025-11-28T10:30:00Z"
}
```

---

## Observability

### CloudWatch Dashboards

Key metrics to monitor:

- `hls_videos_processed_total{status="success|failed"}` - Processing success rate
- `hls_video_processing_duration_seconds` - Transcoding time by resolution
- `ApproximateNumberOfMessagesVisible` - Queue depth
- `ECSServiceAverageCPUUtilization` - Service resource usage

### Alarms Configured

| Alarm | Threshold | Action |
|-------|-----------|--------|
| DLQ Messages | > 0 | SNS notification |
| Queue Depth | > 50 | SNS notification |
| Message Age | > 1 hour | SNS notification |

### Tracing

Access distributed traces in AWS X-Ray console:
1. Navigate to CloudWatch → X-Ray traces
2. Filter by service: `hls-api` or `hls-worker`
3. View end-to-end request flow

---

## CI/CD Pipeline

```
┌────────────┐    ┌────────────┐    ┌────────────┐    ┌────────────┐
│   Push to  │───►│  Security  │───►│   Build &  │───►│  Deploy to │
│    main    │    │   Scans    │    │    Test    │    │    ECS     │
└────────────┘    └────────────┘    └────────────┘    └────────────┘
                        │                 │                 │
                        ▼                 ▼                 ▼
                    - gosec           - go test        - ECR push
                    - Trivy           - go lint        - Task def update
                    - CodeQL          - Race detect    - Rolling deploy
```

### GitHub Actions Workflow

The pipeline (`.github/workflows/deploy.yml`) executes:

1. **Security Scan**: gosec static analysis
2. **Unit Tests**: `go test -race -cover`
3. **Linting**: golangci-lint
4. **Build**: Multi-stage Docker builds
5. **Vulnerability Scan**: Trivy container scanning
6. **Deploy**: ECS task definition update with rollback

### OIDC Authentication

Uses GitHub Actions OIDC provider for keyless AWS authentication - no long-lived credentials stored in GitHub secrets.

---

## Local Development

```bash
# Start local environment (LocalStack + observability stack)
make local-up

# Services available:
# - API:        http://localhost:8080
# - Jaeger UI:  http://localhost:16686
# - Prometheus: http://localhost:9090

# View logs
make local-logs

# Stop environment
make local-down
```

---

## Project Structure

```
.
├── cmd/
│   ├── api/           # API server entrypoint
│   └── worker/        # Worker service entrypoint
├── internal/
│   ├── auth/          # JWT authentication
│   ├── handlers/      # HTTP handlers
│   ├── logger/        # Structured logging with trace context
│   ├── observability/ # OpenTelemetry setup
│   └── storage/       # S3 client wrapper
├── infra/
│   ├── bootstrap/     # Terraform state infrastructure
│   └── environments/
│       └── dev/       # Development environment
│           ├── alb.tf
│           ├── cdn.tf
│           ├── compute.tf
│           ├── dns.tf
│           ├── ecr.tf
│           ├── iam.tf
│           ├── network.tf
│           ├── queue.tf
│           └── storage.tf
├── configs/           # Local development configs
├── scripts/           # Utility scripts
├── Dockerfile         # API container
├── Dockerfile.worker  # Worker container
├── Makefile          # Build automation
└── index.html        # Debug player
```

---
