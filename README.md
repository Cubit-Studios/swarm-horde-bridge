# Swarm-Horde Bridge

A middleware service that bridges Helix Swarm code reviews with Epic's Horde CI/CD system.

## Features

- Automatic job creation in Horde when Swarm reviews are created/updated
- Real-time status updates from Horde to Swarm
- HTTP API for monitoring and management
- Configurable via YAML, environment variables, or command line flags
- Prometheus metrics endpoint
- Structured logging
- Health check endpoint
- Docker support
- Systemd service support

## Prerequisites

- Go 1.21 or later
- Helix Swarm instance
- Epic's Horde CI/CD system
- Access tokens for both systems

## Quick Start

1. Clone the repository:
```bash
git clone https://github.com/Cubit-Studios/swarm-horde-bridge.git
cd swarm-horde-bridge
```

2. Copy and edit the configuration:
```bash
cp config.yaml.example config.yaml
# Edit config.yaml with your settings
```

2. Get dependencies:
```bash
go mod tidy
```

4. Build the service:
```bash
# Linux/MacOS
./scripts/build.sh

# Windows
scripts\build.bat
```

4. Run the service:
```bash
./swarm-horde-bridge
```

## Configuration

The service can be configured through:
- Configuration file (config.yaml)
- Environment variables
- Command line flags

### Environment Variables

- `PORT` - Server port (default: 8080)
- `SWARM_HOST` - Swarm server URL
- `HORDE_HOST` - Horde server URL
- `HORDE_KEY` - Horde API key
- `LOG_LEVEL` - Logging level (default: info)

### API Endpoints

- `GET /health` - Health check endpoint
- `POST /webhook/swarm-test` - Swarm webhook endpoint
- `GET /metrics` - Prometheus metrics endpoint
- `GET /jobs` - List current jobs

## Monitoring

The service exposes Prometheus metrics at `/metrics` including:
- Request counts and latencies
- Job processing statistics
- System metrics

## Development

### Running Tests
```bash
go test ./...
```

### Running Linter
```bash
golangci-lint run
```

### Building for Different Platforms
```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o swarm-horde-bridge ./cmd/server

# Windows
GOOS=windows GOARCH=amd64 go build -o swarm-horde-bridge.exe ./cmd/server
```

## Contributing

1. Fork the repository
2. Create your feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## License

This project is licensed under the MIT License
