FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/tvtl ./cmd/tvtl

# Serviço lipsync: Rhubarb Lip Sync (MIT) + o binário tvtl como servidor HTTP.
FROM debian:bookworm-slim AS lipsync
ARG RHUBARB=https://github.com/DanielSWolf/rhubarb-lip-sync/releases/download/v1.14.0/Rhubarb-Lip-Sync-1.14.0-Linux.zip
RUN apt-get update && apt-get install -y --no-install-recommends curl unzip ca-certificates \
 && curl -fsSL -o /tmp/r.zip "$RHUBARB" && unzip -q /tmp/r.zip -d /opt && mv /opt/Rhubarb-Lip-Sync-1.14.0-Linux /opt/rhubarb \
 && rm /tmp/r.zip && apt-get purge -y curl unzip && apt-get autoremove -y && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/tvtl /usr/local/bin/tvtl
RUN useradd -r -u 10003 lip
USER lip
ENTRYPOINT ["tvtl"]
CMD ["lipsync-server", "--listen", ":8080", "--rhubarb", "/opt/rhubarb/rhubarb"]

# Imagem principal: tvtl + ffmpeg (codificação Opus/MP3 e render).
FROM alpine:3.20 AS tvtl
RUN apk add --no-cache ffmpeg && adduser -D -H -u 10001 tvtl
COPY --from=build /out/tvtl /usr/local/bin/tvtl
COPY config /app/config
WORKDIR /app
USER tvtl
ENTRYPOINT ["tvtl"]
CMD ["run"]
