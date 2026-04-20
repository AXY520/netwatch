FROM golang:1.22-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN go build -trimpath -ldflags="-s -w" -o /out/netwatch ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata traceroute mtr \
    && adduser -D -H -u 10001 netwatch
WORKDIR /app
COPY --from=build /out/netwatch /app/netwatch
COPY web /app/web
RUN mkdir -p /app/data && chown -R netwatch:netwatch /app
USER netwatch
EXPOSE 8080
ENV TZ=Asia/Shanghai \
    PORT=8080 \
    DATA_DIR=/app/data
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:${PORT:-8080}/healthz || exit 1
CMD ["/app/netwatch"]
