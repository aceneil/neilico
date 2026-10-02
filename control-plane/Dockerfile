# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/neilico-api ./cmd/api

FROM alpine:3.21
RUN apk add --no-cache ca-certificates curl \
    && addgroup -S neilico && adduser -S -G neilico -h /var/lib/neilico neilico \
    && mkdir -p /app/configs /app/data /var/lib/neilico \
    && chown -R neilico:neilico /app /var/lib/neilico
WORKDIR /app
COPY --from=build /out/neilico-api /usr/local/bin/neilico-api
COPY configs/config.example.yaml /app/configs/config.example.yaml
USER neilico
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/neilico-api"]
CMD ["--config", "/app/configs/config.example.yaml"]
