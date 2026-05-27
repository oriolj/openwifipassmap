# syntax=docker/dockerfile:1
# Multi-stage build for the WiFi Spots backend (API + public web).
# Pure-Go SQLite (modernc) → CGO disabled → tiny static binary.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache wget ca-certificates && \
    adduser -D -u 10001 app && \
    mkdir -p /data && chown app /data
USER app
WORKDIR /app
COPY --from=build /out/server /app/server

ENV ADDR=:8080 \
    DB_PATH=/data/wifispot.db
EXPOSE 8080
VOLUME ["/data"]

# Blue-green friendly: container is healthy only once the API answers.
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health || exit 1

STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/server"]
