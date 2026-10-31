# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------
# Stage 1: Build stage (Native Go cross-compilation - Ultra-Fast, No QEMU lag)
# ---------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:alpine AS builder

ARG TARGETOS TARGETARCH

WORKDIR /app

# Download dependencies first for optimal Docker layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy only Go source code (avoid copying 300MB web/ assets into builder)
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Native Go cross-compilation (compiles ARM64 & AMD64 in seconds instead of 15m in QEMU)
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w" -o paperless-go ./cmd/server

# ---------------------------------------------------------------------
# Stage 2: Final minimal runtime stage
# ---------------------------------------------------------------------
FROM alpine:3.21

# Install certificates, timezone data, PDF utilities and OCR support (Portuguese + English)
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    poppler-utils \
    tesseract-ocr \
    tesseract-ocr-data-por \
    tesseract-ocr-data-eng

# Create unprivileged user and group for security hardening
RUN addgroup -g 1000 -S appgroup && \
    adduser -u 1000 -S appuser -G appgroup -h /app -s /bin/sh

WORKDIR /app

# Copy compiled Go binary and web static assets (Angular SPA)
COPY --from=builder /app/paperless-go /app/paperless-go
COPY web /app/web

# Create required runtime data, media, and consume directories with correct ownership
RUN mkdir -p /app/data /app/media /app/consume && \
    chown -R appuser:appgroup /app

# Run as non-privileged user
USER appuser

# Environment defaults
ENV PAPERLESS_PORT=8000
ENV PAPERLESS_HOST=0.0.0.0
ENV PAPERLESS_DATA_DIR=/app/data
ENV PAPERLESS_MEDIA_DIR=/app/media
ENV PAPERLESS_CONSUME_DIR=/app/consume
ENV PAPERLESS_STATIC_DIR=/app/web/static

# Persistent data volumes
VOLUME ["/app/data", "/app/media", "/app/consume"]

# Healthcheck for container orchestrators (Docker, Kubernetes, Portainer)
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8000/accounts/login/ || exit 1

EXPOSE 8000

ENTRYPOINT ["/app/paperless-go"]
