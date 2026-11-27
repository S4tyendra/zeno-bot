FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates tzdata curl aria2 ffmpeg python3 tar gzip fonts-dejavu-core \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://github.com/containerd/nerdctl/releases/download/v2.1.2/nerdctl-2.1.2-linux-amd64.tar.gz \
        | tar -xz -C /usr/local/bin nerdctl \
    && curl -fsSL https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp \
    && chmod a+rx /usr/local/bin/yt-dlp \
    && curl -fsSL https://github.com/h4ckf0r0day/obscura/releases/download/v0.2.7/obscura-x86_64-linux-stealth.tar.gz \
        | tar -xz -C /usr/local/bin \
    && chmod a+rx /usr/local/bin/obscura /usr/local/bin/obscura-worker

WORKDIR /app

# Copy the pre-built binary from the host
COPY dist/zeno ./zeno

RUN mkdir -p /app/data /app/generated /app/data/profiles

ENTRYPOINT ["./zeno"]
