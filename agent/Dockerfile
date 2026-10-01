# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/umpp-agent ./cmd/agent

FROM alpine:3.21
RUN apk add --no-cache iproute2 wireguard-tools ca-certificates \
    && addgroup -S umpp && adduser -S -G umpp -h /var/lib/umpp-agent umpp \
    && mkdir -p /var/lib/umpp-agent /run/umpp-agent \
    && chown -R umpp:umpp /var/lib/umpp-agent /run/umpp-agent
COPY --from=build /out/umpp-agent /usr/local/bin/umpp-agent
USER umpp
ENTRYPOINT ["/usr/local/bin/umpp-agent"]
CMD ["--config", "/etc/umpp-agent/agent.yaml"]

# Real network changes require Linux capabilities and a TUN device:
#   docker run --cap-add NET_ADMIN --cap-add NET_RAW \
#     --device /dev/net/tun -v ./agent.yaml:/etc/umpp-agent/agent.yaml:ro umpp-agent
# The default non-root image can run with --dry-run without those privileges.
