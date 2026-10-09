#!/bin/sh
set -e
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o swarm-horde-bridge ./cmd/server
echo "Build completed successfully"
