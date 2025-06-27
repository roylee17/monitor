# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.23-bookworm AS builder

# Enable BuildKit inline cache
ARG BUILDKIT_INLINE_CACHE=1

# Install build dependencies
RUN apt-get update && apt-get install -y \
    git \
    make \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./

# Download dependencies with cache mount
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download && go mod verify

# Copy source code
COPY . .

# Build the monitor binary with cache mount
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -trimpath -o monitor ./cmd/monitor

# Runtime stage
FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    jq \
    curl \
    bash \
    wget \
    && rm -rf /var/lib/apt/lists/*

# Copy interchain-security-pd binary from ICS image
COPY --from=ghcr.io/cosmos/interchain-security:v7.0.1 /usr/local/bin/interchain-security-pd /usr/local/bin/interchain-security-pd

# Copy the monitor binary
COPY --from=builder /app/monitor /usr/local/bin/monitor

# Make binary executable
RUN chmod +x /usr/local/bin/monitor

# Create necessary directories
RUN mkdir -p /data

# Set working directory
WORKDIR /data

# Default command
CMD ["/usr/local/bin/monitor"]