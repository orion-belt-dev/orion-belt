# Build stage
FROM golang:1.27.3-alpine@sha256:d530cf60723abf1ffae0cd16bb546b379ec0550d2f3815e5b16ed69dbc00fba4 AS builder

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
FROM alpine:3.24@sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35 AS server

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
