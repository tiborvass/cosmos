FROM node:24.1.0-slim@sha256:5ae787590295f944e7dc200bf54861bac09bf21b5fdb4c9b97aee7781b6d95a2 AS claude
RUN --mount=type=cache,target=/root/.npm npm install -g @anthropic-ai/claude-code
ENTRYPOINT ["/usr/local/claude"]

FROM --platform=$BUILDPLATFORM golang:1.23 AS builder
ARG BUILDPLATFORM
ARG TARGETARCH
WORKDIR /w
ENV GOARCH=${TARGETARCH}
RUN --mount=target=/w --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -o /tmp/coding-proxy ./coding-proxy

FROM alpine:latest AS coding-proxy
RUN apk add --no-cache docker-cli
COPY --from=builder /tmp/coding-proxy .
EXPOSE 8080
ENTRYPOINT ["/coding-proxy"]
