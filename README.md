# Cosmos

A proxy wrapper for Claude CLI that intercepts and logs all API traffic for debugging.

## Architecture

```
┌─────────────────────┐
│   Host (main.go)    │
│                     │
│  Spawns container   │
└──────────┬──────────┘
           │
┌──────────▼──────────┐
│   Docker Container  │
│                     │
│  ┌───────────────┐  │
│  │ Proxy (:8080) │  │
│  └───────┬───────┘  │
│          │          │
│  ┌───────▼───────┐  │
│  │   Claude CLI  │  │
│  └───────────────┘  │
└─────────────────────┘
```

- Proxy intercepts all API calls to `api.anthropic.com`
- Logs are written to `/tmp/cosmos-proxy.log` inside the container
- Claude's TUI remains clean and interactive

## Setup

```bash
# Build the container
./build.sh

# Run Claude with proxy
go run main.go claude [args...]
```

## Development Workflow

### 1. Start Claude
```bash
# Terminal 1 - run Claude
go run main.go claude
```

### 2. Watch Proxy Logs
```bash
# Terminal 2 - tail logs from running container
./dev-tail-logs.sh

# Clear logs and tail
./dev-tail-logs.sh --clear
```

### 3. Rebuild After Changes
```bash
# Stop containers and rebuild
./dev-rebuild.sh

# Then start Claude again (step 1)
```

## Debugging

```bash
# Access running container
docker ps  # Find container ID
docker exec -it <container-id> /bin/bash

# Inside container:
ps aux                      # See proxy and claude processes
cat /tmp/cosmos-proxy.log   # View proxy logs
curl http://localhost:8080  # Test proxy directly

# Extract logs from container (running or stopped)
./dev-extract-logs.sh       # Copies logs to host with timestamp
```

## Files

- `main.go` - Host CLI that spawns the container
- `proxy/` - HTTP proxy that intercepts API calls
- `entrypoint/` - Container entrypoint that starts proxy + claude
- `Dockerfile` - Builds container with both components
- `/tmp/cosmos-proxy.log` - Proxy logs (inside container)