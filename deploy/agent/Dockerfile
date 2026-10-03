# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY agent/go.mod agent/go.sum ./agent/
COPY control-plane/go.mod control-plane/go.sum ./control-plane/
RUN cd /src/agent && go mod download
COPY agent ./agent
COPY control-plane ./control-plane
ARG VERSION=dev
RUN cd /src/agent && CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" -o /out/neilico-agent ./cmd/agent

FROM alpine:3.21
RUN apk add --no-cache ca-certificates iproute2 wireguard-tools \
    && addgroup -S neilico \
    && adduser -S -G neilico -h /var/lib/neilico-agent neilico \
    && mkdir -p /var/lib/neilico-agent \
    && chown neilico:neilico /var/lib/neilico-agent
COPY --from=build /out/neilico-agent /usr/local/bin/neilico-agent

ENV NEILICO_STATE_DIR=/var/lib/neilico-agent
VOLUME ["/var/lib/neilico-agent"]
ENTRYPOINT ["/usr/local/bin/neilico-agent"]
CMD ["run"]
