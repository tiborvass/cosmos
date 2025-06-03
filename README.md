# Cosmos - Container Management System

A lightweight Docker-based container orchestration tool that simplifies container lifecycle management through a CLI interface and intelligent proxy system.

## Key Features

- 🚀 **Simple CLI**: Single binary for all container operations
- 🔄 **Smart Proxy Reuse**: Automatically reuses existing proxy containers
- 🌐 **Isolated Networking**: All containers run in isolated `cosmos-net` network
- 🖥️ **TTY Auto-detection**: Seamlessly handles interactive and batch modes
- 🛡️ **Graceful Fallbacks**: Auto-retries with `/bin/sh` if `/bin/bash` unavailable
- ⚡ **Fast Operations**: Minimal overhead with efficient container management

## Architecture

- **CLI (`main.go`)**: Lightweight client that communicates with proxy
- **Proxy (`coding-proxy/`)**: HTTP server managing Docker operations
- **Network**: Isolated `cosmos-net` Docker network for all containers
- **Container Lifecycle**: Automatic exec into containers after creation

### Proxy Architecture Details

The proxy container (`tiborvass/coding-proxy`) serves two main functions:

1. **Container Management API** (`/container` endpoint):
   - Receives JSON commands from the CLI
   - Executes Docker commands to manage containers
   - Returns container IDs and status information

2. **HTTP Reverse Proxy** (all other endpoints):
   - Logs and debugs HTTP requests/responses
   - Can be extended to proxy requests to managed containers

**Container Management Flow:**
```
CLI → HTTP POST /container → Proxy → Docker Socket → Container
```

**Proxy Container Features:**
- Runs in Alpine Linux for minimal footprint
- Has Docker CLI installed and socket access (`/var/run/docker.sock`)
- Listens on port 8080 inside container, mapped to host port 8080
- Automatically joins the `cosmos-net` network

## Quick Start

### Building
```bash
# Build the CLI
go build -o cosmos .

# Build the proxy Docker image  
docker build -t tiborvass/coding-proxy -f Dockerfile --target coding-proxy .
```

### Basic Usage
```bash
# Start an interactive Ubuntu container
./cosmos start ubuntu:latest

# Start a container with a specific command
./cosmos start alpine:latest echo "Hello World"

# Stop a container
./cosmos stop <container_id>

# Restart a container
./cosmos restart <container_id>
```

## Testing

### 1. Basic Container Operations

**Start a container with a command:**
```bash
./cosmos start ubuntu:latest echo "Hello World"
```
Expected output:
```
Spawning coding-proxy container...
Proxy container started: abc123def456
Container started: 925d367807f8
Hello World
```

**Start an interactive container:**
```bash
./cosmos start ubuntu:latest
```
Expected output:
```
Spawning coding-proxy container...
Proxy container started: abc123def456
Container started: 749624501d02
root@749624501d02:/# 
```
This creates a container with `tail -f /dev/null` to keep it running, then automatically executes `/bin/bash` to provide an interactive shell.

**Stop a container:**
```bash
./cosmos stop <container_id>
```
Expected output:
```
Container stopped: <container_id>
```

**Restart a container:**
```bash
./cosmos restart <container_id>
```
Expected output:
```
Container restarted: <container_id>
```

### 2. Verify Container Execution

Check that your container actually ran:
```bash
# Get container ID from previous start command
docker logs <container_id>
```

For the "Hello World" example, you should see:
```
Hello World
```

### 3. Test Proxy Reuse

The system reuses an existing proxy if one is running:

```bash
# First command spawns proxy
./cosmos start ubuntu:latest echo "First container"

# Second command reuses existing proxy
./cosmos start alpine:latest echo "Second container"
```

Only the first command should show "Spawning coding-proxy container...".

### 4. Test Network Connectivity

Verify containers are in the same network:
```bash
# Start a long-running container
./cosmos start nginx:alpine

# Check the network
docker network inspect cosmos-net
```

You should see both the proxy container and your application containers listed.

### 5. Real-world Examples

**Development environment with Ubuntu:**
```bash
# Start an Ubuntu container for development
./cosmos start ubuntu:latest

# Inside the container, you can install tools:
apt update && apt install -y python3 python3-pip git
python3 --version
```

**Running a web server:**
```bash
# Start nginx in detached mode
./cosmos start nginx:alpine nginx -g "daemon off;"

# Test from another container
./cosmos start ubuntu:latest curl http://<nginx_container_ip>
```

**Using different distributions:**
```bash
# Alpine Linux (minimal)
./cosmos start alpine:latest

# CentOS/RHEL
./cosmos start centos:latest

# Debian
./cosmos start debian:latest
```

### 6. Test HTTP API Directly

You can test the proxy API directly:

```bash
# Start a container via API
curl -X POST http://localhost:8080/container \
  -H "Content-Type: application/json" \
  -d '{"action":"start","image":"ubuntu:latest","args":["echo","Direct API test"]}'

# Stop a container via API  
curl -X POST http://localhost:8080/container \
  -H "Content-Type: application/json" \
  -d '{"action":"stop","container_id":"<container_id>"}'
```

### 7. Cleanup

Remove the proxy container and network:
```bash
docker stop cosmos-proxy
docker rm cosmos-proxy
docker network rm cosmos-net
```

## Usage

```
cosmos <action> [args...]

Actions:
  start <image> [args...]  - Start a new container
  stop <container_id>      - Stop a container  
  restart <container_id>   - Restart a container
```

## Features

- **Docker Socket Access**: Proxy has access to Docker socket for container management
- **Shared Network**: All containers run in `cosmos-net` network
- **Parallel Connections**: Proxy handles multiple simultaneous requests
- **Auto-Exec**: CLI automatically execs into newly created containers
- **Proxy Reuse**: Existing proxy containers are reused across CLI invocations
- **Smart Container Lifecycle**: Containers without commands use `tail -f /dev/null` to stay alive
- **TTY Detection**: Automatically handles interactive vs non-interactive environments
- **Cross-platform**: Works on Linux, macOS, and Windows with Docker

## Troubleshooting

### Common Issues

| Issue | Cause | Solution |
|-------|-------|----------|
| "connection refused" | Proxy not ready | Wait a few seconds, check `docker logs cosmos-proxy` |
| "executable file not found" | Missing Docker CLI | Rebuild: `docker build -t tiborvass/coding-proxy -f Dockerfile --target coding-proxy .` |
| "network not found" | Network creation failed | Manually create: `docker network create cosmos-net` |
| "Docker not available" | Docker daemon not running | Start Docker daemon: `sudo systemctl start docker` |
| Exec failures | Shell not available | Cosmos auto-retries with `/bin/sh` if `/bin/bash` fails |

### Debug Mode
```bash
# Check proxy logs
docker logs cosmos-proxy

# Verify network
docker network inspect cosmos-net

# List running containers
docker ps --filter network=cosmos-net
```