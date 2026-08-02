FROM golang:1.26-alpine3.22 AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/morph-sandbox ./cmd/morph-sandbox
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /morph-sandbox ./cmd/morph-sandbox

FROM alpine:3.22
RUN apk add --no-cache ca-certificates git patch ripgrep && \
    addgroup -g 65532 morph && \
    adduser -D -H -u 65532 -G morph morph && \
    mkdir -p /workspace /home/morph /run/morph && \
    chown -R 65532:65532 /workspace /home/morph /run/morph /tmp
COPY --from=builder --chmod=0555 /morph-sandbox /usr/local/bin/morph-sandbox
USER 65532:65532
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/morph-sandbox"]
CMD ["/bin/sh"]
