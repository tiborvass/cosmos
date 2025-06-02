FROM golang:1.24.3@sha256:81bf5927dc91aefb42e2bc3a5abdbe9bb3bae8ba8b107e2a4cf43ce3402534c6 AS builder
WORKDIR /w
RUN --mount=target=/w --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -o /tmp/coding-proxy .

FROM scratch AS coding-proxy
COPY --from=builder /tmp/coding-proxy .
EXPOSE 8080
ENTRYPOINT ["/coding-proxy"]
