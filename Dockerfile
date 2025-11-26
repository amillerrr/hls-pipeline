# Stage 1: Builder
FROM golang:1.25.4-alpine AS builder

# Install git 
RUN apk add --no-cache git

WORKDIR /app

# Download dependencies 
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the API binary
RUN CGO_ENABLED=0 GOOS=linux go build -o api-server ./cmd/api

# Stage 2: Runner
FROM alpine:latest

# Install certificates for HTTPS requests 
RUN apk add --no-cache ca-certificates

WORKDIR /root/

# Copy the binary from the builder stage
COPY --from=builder /app/api-server .

# Expose the port defined in main.go
EXPOSE 8080

# Run the binary
CMD ["./api-server"]
