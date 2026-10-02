# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/neilico-agent ./cmd/agent

FROM alpine:3.21
RUN apk add --no-cache iproute2 wireguard-tools ca-certificates \
    && addgroup -S neilico && adduser -S -G neilico -h /var/lib/neilico-agent neilico \
    && mkdir -p /var/lib/neilico-agent /run/neilico-agent \
    && chown -R neilico:neilico /var/lib/neilico-agent /run/neilico-agent
COPY --from=build /out/neilico-agent /usr/local/bin/neilico-agent
USER neilico
ENTRYPOINT ["/usr/local/bin/neilico-agent"]
CMD ["--config", "/etc/neilico-agent/agent.yaml"]

# Real network changes require Linux capabilities and a TUN device:
#   docker run --cap-add NET_ADMIN --cap-add NET_RAW \
#     --device /dev/net/tun -v ./agent.yaml:/etc/neilico-agent/agent.yaml:ro neilico-agent
# The default non-root image can run with --dry-run without those privileges.
