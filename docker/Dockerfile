# syntax=docker/dockerfile:1
# Multi-stage build for the OpenWifiPassMap backend (API + public web).
# Pure-Go SQLite (modernc) → CGO disabled → tiny static binary.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/server ./cmd/server

# Compile Tailwind+DaisyUI and vendor leaflet (templates are scanned for class
# names, so they're copied alongside). Output: /internal/web/static.
FROM node:22-alpine AS assets
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY web/ ./
COPY internal/web/templates/ /internal/web/templates/
RUN npm run build

FROM alpine:3.20
RUN apk add --no-cache wget ca-certificates && \
    adduser -D -u 10001 app && \
    mkdir -p /data && chown app /data
USER app
WORKDIR /app
COPY --from=build /out/server /app/server
# Litestream: continuous SQLite replication to S3-compatible storage. The
# entrypoint activates it only when LITESTREAM_ACCESS_KEY_ID is set, so the
# image runs fine locally without credentials.
COPY --from=litestream/litestream:0.3 /usr/local/bin/litestream /usr/local/bin/litestream
COPY --chmod=755 docker/entrypoint.sh /app/entrypoint.sh
COPY docker/litestream.yml /etc/litestream.yml
COPY --from=assets /internal/web/static /app/static

ENV ADDR=:8080 \
    DB_PATH=/data/wifispot.db \
    STATIC_DIR=/app/static
EXPOSE 8080
VOLUME ["/data"]

# Blue-green friendly: container is healthy only once the API answers.
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health || exit 1

STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/entrypoint.sh"]
