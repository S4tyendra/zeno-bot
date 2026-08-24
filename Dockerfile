FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata docker-cli curl && \
    curl -fsSL https://github.com/containerd/nerdctl/releases/download/v2.1.2/nerdctl-2.1.2-linux-amd64.tar.gz \
    | tar -xz -C /usr/local/bin nerdctl && \
    chmod +x /usr/local/bin/nerdctl

WORKDIR /app

# Copy the pre-built binary from the host
COPY dist/zeno ./zeno

RUN mkdir -p /app/data /app/generated

ENTRYPOINT ["./zeno"]
