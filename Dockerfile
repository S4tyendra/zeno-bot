FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata docker-cli

WORKDIR /app

# Copy the pre-built binary from the host
COPY dist/zeno ./zeno

RUN mkdir -p /app/data /app/generated

ENTRYPOINT ["./zeno"]
