# Build stage
FROM golang:1.26.6-alpine AS builder

# Go tools + Node (React console → web/static, embedded via go:embed)
RUN apk add --no-cache git make nodejs npm

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# build-ui then binaries; UI lands in web/static and is compiled into the server.
# Plugins (audit-logger, slack/email/webhook notifications, chatops-access-request)
# are compiled directly into orion-belt-server — no separate build step.
RUN make build

# Server image
FROM alpine:latest AS server

RUN apk add --no-cache ca-certificates openssh-keygen

WORKDIR /app

COPY --from=builder /build/bin/orion-belt-server /app/
COPY docker/server-entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Create directories. The SSH host key is deliberately NOT generated here —
# baking it into the image would give every container from this image the
# same host key, defeating host-key verification. entrypoint.sh generates a
# unique one on first run and persists it in the mounted volume instead.
RUN mkdir -p /var/lib/orion-belt/recordings /etc/orion-belt

EXPOSE 2222 8080

ENTRYPOINT ["/app/entrypoint.sh"]
