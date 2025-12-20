# Stage 1: Builder
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Copy go mod files
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /worker ./cmd/worker

# Final stage
FROM alpine:3.22.2

WORKDIR /app

# Install runtime dependencies including FFmpeg
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    ffmpeg

# Copy binary from builder
COPY --from=builder /worker /app/worker

# Create temp directories
RUN mkdir -p /tmp/uploads /tmp/hls

# Create non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
RUN chown -R appuser:appgroup /tmp/uploads /tmp/hls
USER appuser

# Expose metrics port
EXPOSE 2112

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:2112/health || exit 1

# Run the binary
ENTRYPOINT ["/app/worker"]
