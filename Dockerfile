FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
WORKDIR /src
COPY . .
ENV CGO_ENABLED=0 GOEXPERIMENT=jsonv2
RUN go mod download && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -tags "sing xray hysteria2 with_quic with_grpc with_utls with_wireguard with_acme with_gvisor" \
      -ldflags "-s -w -X github.com/shmily2-1/V2bX-2/cmd.version=$VERSION" -o /out/V2bX .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata nftables && mkdir -p /etc/V2bX
COPY --from=builder /out/V2bX /usr/local/bin/V2bX
ENTRYPOINT ["V2bX", "server", "--config", "/etc/V2bX/config.json"]
