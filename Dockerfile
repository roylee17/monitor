# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.23-bookworm AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y \
    git \
    make \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the monitor binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o monitor ./cmd/monitor

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