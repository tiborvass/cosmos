FROM --platform=$BUILDPLATFORM golang:1.24.3@sha256:81bf5927dc91aefb42e2bc3a5abdbe9bb3bae8ba8b107e2a4cf43ce3402534c6 AS builder
ARG BUILDPLATFORM
ARG TARGETARCH
WORKDIR /w
ENV GOARCH=${TARGETARCH}
# Build proxy
RUN --mount=target=/w --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -o /tmp/cosmos-proxy ./proxy

FROM node:24.1.0-slim@sha256:5ae787590295f944e7dc200bf54861bac09bf21b5fdb4c9b97aee7781b6d95a2 AS cosmos
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/* /tmp/*
RUN --mount=type=cache,target=/root/.npm npm install -g @anthropic-ai/claude-code && rm -rf /tmp/* && sed -E -i'' 's/(\|\|process\.env\.API_TIMEOUT_MS\|\|process\.env\.MAX_THINKING_TOKENS)\|\|process\.env\.ANTHROPIC_BASE_URL/\1/' /usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js
RUN useradd -ms /bin/bash cosmos
USER cosmos
COPY --chown=cosmos:cosmos cosmos-proxy /usr/local/bin/cosmos-proxy
#COPY --from=builder --chown=cosmos:cosmos /tmp/cosmos-proxy /usr/local/bin/cosmos-proxy
RUN mkdir ~/.claude
EXPOSE 8042
ENTRYPOINT ["/usr/local/bin/cosmos-proxy"]
