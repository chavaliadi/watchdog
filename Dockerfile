# Stage 1: Build binary
FROM golang:1.26-alpine AS builder

WORKDIR /src

# ca-certificates is required for HTTPS outbound checks and webhooks
RUN apk add --no-cache ca-certificates

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy application source
COPY cmd/ cmd/
COPY internal/ internal/

# Build static binary without CGO
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /bin/watchdog \
    ./cmd/watchdog

# Stage 2: Minimal, non-root runtime image
FROM alpine:3.21

# Install runtime certificates for outbound TLS checks and webhook delivery
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S watchdog -g 10001 \
    && adduser -S -G watchdog -u 10001 watchdog

WORKDIR /app

COPY --from=builder /bin/watchdog /app/watchdog

USER watchdog:watchdog

EXPOSE 8080

ENTRYPOINT ["/app/watchdog"]
