# Rotaze - Self-healing SOCKS5 proxy with automatic IP rotation
# Multi-stage build for minimal image size

# Build stage
FROM golang:1.25.5-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

# Copy go.mod and go.sum first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build with optimizations
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o rotaze ./cmd/rotaze

# Install sing-box
RUN apk add --no-cache curl && \
    curl -sL https://github.com/SagerNet/sing-box/releases/download/v1.13.19/sing-box-1.13.19-linux-amd64.tar.gz | tar xz && \
    mv sing-box-*/sing-box /usr/local/bin/sing-box && \
    rm -rf sing-box-* && \
    apk del curl

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -g '' rotaze

# Copy binaries
COPY --from=builder /usr/local/bin/sing-box /usr/local/bin/sing-box
COPY --from=builder /build/rotaze /usr/local/bin/rotaze

# Create data directory
RUN mkdir -p /home/rotaze/.rotaze && \
    chown -R rotaze:rotaze /home/rotaze

USER rotaze
WORKDIR /home/rotaze

# Expose SOCKS5 port
EXPOSE 1080

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD rotaze daemon status || exit 1

# Default command
ENTRYPOINT ["rotaze"]
CMD ["run", "--url", "https://api.ipify.org"]
