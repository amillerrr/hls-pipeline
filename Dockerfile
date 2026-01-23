# syntax=docker/dockerfile:1

# Multi-stage build for API server and Worker
# Build Stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go.mod and go.sum first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build arguments
ARG BUILD_TARGET=api
ARG VERSION=dev
ARG COMMIT=unknown

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.Commit=${COMMIT}" \
    -o /app/bin/${BUILD_TARGET} \
    ./cmd/${BUILD_TARGET}

# Production Stage - API
FROM alpine:3.22.2 AS api

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 app && \
    adduser -u 1000 -G app -s /bin/sh -D app

WORKDIR /app

# Copy binary
COPY --from=builder /app/bin/api /app/api

# Copy configs if needed
COPY --from=builder /app/configs /app/configs

# Set ownership
RUN chown -R app:app /app

USER app

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

EXPOSE 8080

ENTRYPOINT ["/app/api"]

# Production Stage - Worker
FROM alpine:3.22.2 AS worker

# Install runtime dependencies including FFmpeg
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    ffmpeg \
    wget

# Create non-root user
RUN addgroup -g 1000 app && \
    adduser -u 1000 -G app -s /bin/sh -D app

# Create temp directory with proper permissions
RUN mkdir -p /tmp/hls-pipeline && chown -R app:app /tmp/hls-pipeline

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/bin/worker /app/worker

# Copy configs
COPY --from=builder /app/configs /app/configs

# Set ownership
RUN chown -R app:app /app

USER app

# Environment
ENV TEMP_DIR=/tmp/hls-pipeline

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:2112/health || exit 1

EXPOSE 2112

ENTRYPOINT ["/app/worker"]

# Development Stage (includes both targets + tools)
FROM golang:1.24-alpine AS dev

RUN apk add --no-cache \
    git \
    ffmpeg \
    wget \
    curl \
    bash

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

# Install dev tools
RUN go install github.com/air-verse/air@latest && \
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

COPY . .

# Default to API
CMD ["air", "-c", ".air.toml"]

