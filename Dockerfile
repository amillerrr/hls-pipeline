# Stage 1: Builder
FROM golang:1.25.4-alpine AS builder

WORKDIR /app

# Download dependencies 
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build the API binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o api-server ./cmd/api

# Stage 2: Runner
FROM alpine:3.22.2

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata wget

WORKDIR /app

RUN addgroup -g 1000 appgroup && \
  adduser -u 1000 -G appgroup -D -h /app appuser && \
  chown -R appuser:appgroup /app

# Copy the binary from the builder stage
COPY --from=builder /app/api-server .
RUN chown appuser:appgroup /app/api-server

# Switch to non-root user
USER appuser

# Expose the port defined in main.go
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the binary
CMD ["./api-server"]
