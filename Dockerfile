FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/tvtl ./cmd/tvtl

FROM alpine:3.20
RUN adduser -D -H -u 10001 tvtl
COPY --from=build /out/tvtl /usr/local/bin/tvtl
COPY config /app/config
WORKDIR /app
USER tvtl
ENTRYPOINT ["tvtl"]
CMD ["run"]
