# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/umpp-api ./cmd/api

FROM alpine:3.21
RUN apk add --no-cache ca-certificates curl \
    && addgroup -S umpp && adduser -S -G umpp -h /var/lib/umpp umpp \
    && mkdir -p /app/configs /app/data /var/lib/umpp \
    && chown -R umpp:umpp /app /var/lib/umpp
WORKDIR /app
COPY --from=build /out/umpp-api /usr/local/bin/umpp-api
COPY configs/config.example.yaml /app/configs/config.example.yaml
USER umpp
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/umpp-api"]
CMD ["--config", "/app/configs/config.example.yaml"]
