FROM node:22-bookworm-slim AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --include=dev
COPY web/ ./
RUN npm run build

FROM golang:1.27.4-alpine AS go-builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY VERSION ./
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false -ldflags="-s -w -X github.com/Sakura520222/Sakura-Bot/internal/version.Version=$(tr -d '\r\n' < VERSION)" -o /out/ ./cmd/sakura-main ./cmd/sakura-qa

FROM debian:bookworm-slim
ENV TZ=Asia/Shanghai \
    SAKURA_DOCKER=1 \
    WEBUI_HOST=0.0.0.0 \
    WEBUI_PORT=8080
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates curl tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=go-builder /out/ /usr/local/bin/
COPY --from=web-builder /web/dist /app/web/dist
COPY data/prompt.txt data/poll_prompt.txt data/qa_persona.txt data/config.example.json /opt/sakura/defaults/
COPY internal/guard/opencc/LICENSE internal/guard/opencc/NOTICE /app/third-party/opencc/
COPY LICENSE VERSION CHANGELOG.md ./
COPY docker-entrypoint.sh /usr/local/bin/sakura-entrypoint
RUN sed -i 's/\r$//' /usr/local/bin/sakura-entrypoint \
    && chmod 755 /usr/local/bin/sakura-entrypoint

VOLUME ["/app/data", "/app/logs"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sakura-entrypoint"]
CMD ["sakura-main"]
