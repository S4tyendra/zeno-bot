FROM --platform=$BUILDPLATFORM golang:1.25.6-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

RUN apk add --no-cache git build-base

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-w -s" -o zeno .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -h /app appuser

WORKDIR /app

COPY --from=builder /app/zeno .

RUN mkdir -p /app/data && chown -R appuser:appuser /app

USER appuser

ENTRYPOINT ["./zeno"]
