# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o paperless-go ./cmd/server

# Final stage
FROM alpine:3.20

# Install ca-certificates, tzdata, and lightweight PDF utilities (poppler-utils)
# Optional: add tesseract-ocr and tesseract-ocr-data-por for OCR support
RUN apk add --no-cache ca-certificates tzdata poppler-utils

WORKDIR /app

# Copy binary and web static assets
COPY --from=builder /app/paperless-go /app/paperless-go
COPY web /app/web

# Create required runtime directories
RUN mkdir -p /app/data /app/media /app/consume

ENV PAPERLESS_PORT=8000
ENV PAPERLESS_HOST=0.0.0.0
ENV PAPERLESS_DATA_DIR=/app/data
ENV PAPERLESS_MEDIA_DIR=/app/media
ENV PAPERLESS_CONSUME_DIR=/app/consume
ENV PAPERLESS_STATIC_DIR=/app/web/static

VOLUME ["/app/data", "/app/media", "/app/consume"]

EXPOSE 8000

ENTRYPOINT ["/app/paperless-go"]

