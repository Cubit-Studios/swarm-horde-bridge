@echo off
set GOOS=linux
set GOARCH=amd64
go build -o swarm-horde-bridge ./cmd/server
if %errorlevel% neq 0 exit /b %errorlevel%
echo Build completed successfully