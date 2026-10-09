# Swarm-Horde Bridge

A middleware service that bridges Helix Swarm code reviews with Epic's Horde CI/CD system.

## How it works

1. Swarm calls `POST /webhook/swarm-test` (a Swarm *test definition*) with the
   shelved changelist and the test run update URL. The bridge checks the token
   and the update URL, answers `202 Accepted` immediately, and creates the job
   in the background.
2. The bridge creates a Horde preflight job (`POST /api/v1/jobs`, named
   `Swarm preflight CL <change>`) on the configured stream and template, reports
   `running` to Swarm, then starts tracking the job. If the job cannot be
   created, `fail` is reported to Swarm with the error.
3. A background monitor polls `GET /api/v1/jobs/{id}` every `MONITOR_INTERVAL`
   seconds and reports `running` / `pass` / `fail` back to Swarm using the update URL.
4. Tracked jobs are persisted to `DATA_DIR/jobs.json`, so a restart does not lose
   in-flight jobs: they are resumed at startup and Swarm still gets a final status.
   If a final status cannot be delivered to Swarm, it is retried on the next poll.

### How the verdict is decided

- **Final verdict only once the job is `Complete`.** While a job is `Waiting` or
  `Running`, failed steps or batch errors are not reported as a failure, because
  Horde may still retry the step or replace the batch.
- When `Complete`, the job **fails** if a batch has a fatal error, or a step that
  was not retried has outcome `Failure` or was aborted. Otherwise it **passes**
  (`Success` and `Warnings` both pass).
- Batch errors follow Horde's own rule: `None`, `Incomplete` and `NoLongerNeeded`
  are not fatal (`NoLongerNeeded` is set on healthy jobs when work is superseded,
  e.g. a step was retried); any other value (e.g. `SyncingFailed`) is fatal.
  Steps of `NoLongerNeeded` batches and steps that were retried are ignored.
- Early failures, reported immediately:
  - the job was cancelled by a user (`abortedByUserInfo`, or the deprecated
    `abortedByUser`), with Horde's `cancellationReason` when present;
  - the job no longer exists in Horde (HTTP 404);
  - the job did not finish within `MAX_JOB_AGE` seconds (default 4 hours).
- Unknown job states are never treated as failures; the job keeps being polled.
- Swarm only shows short messages, so the first message is a short summary
  (e.g. `Horde job failed`) and the details follow as extra messages
  (at most 10 messages of at most 80 characters each).

## Features

- Automatic preflight job creation in Horde for Swarm reviews
- Status updates from Horde to Swarm (running / pass / fail, with the failure reason)
- Job store persisted to disk (atomic writes), resumed on restart
- Optional shared-secret authentication of the webhook and an allow-list for Swarm update URLs
- Configurable via environment variables and/or an optional YAML file
- Structured JSON logging (zerolog)
- Health check endpoint, usable as a container health check
- Docker image (multi-stage, distroless, non-root)

## Prerequisites

- Go 1.23 or later (to build from source), or Docker
- Helix Swarm instance
- Epic's Horde (tested against Horde 5.8)
- A Horde service account token. The service account needs:
  - `CreateJob` on the ACLs of the configured stream **and** template
  - `ViewJob` to read job status

## Running with Docker

The image is configured entirely through environment variables; no config file
is required. The image sets `DATA_DIR=/data`.

```bash
docker build -t swarm-horde-bridge .
docker run -d --name swarm-horde-bridge -p 8080:8080 \
  -e HORDE_HOST=https://horde.domain.com \
  -e HORDE_API_KEY=<service account token> \
  -e HORDE_TEMPLATE_ID=<template id> \
  -e HORDE_STREAM_ID=<stream id> \
  -e WEBHOOK_TOKEN=<random secret> \
  -e SWARM_ALLOWED_HOST=swarm.domain.com \
  -v swarm-horde-bridge-data:/data \
  swarm-horde-bridge
```

Docker Compose example (building from a git clone of this repository):

```yaml
services:
  swarm-horde-bridge:
    build: ./swarm-horde-bridge
    restart: unless-stopped
    environment:
      HORDE_HOST: http://horde-server:5000          # internal URL used for API calls
      HORDE_PUBLIC_URL: https://horde.domain.com    # URL used in links shown in Swarm
      HORDE_API_KEY: ${HORDE_API_KEY}
      HORDE_TEMPLATE_ID: my-template
      HORDE_STREAM_ID: my-stream
      WEBHOOK_TOKEN: ${SWARM_WEBHOOK_TOKEN}
      SWARM_ALLOWED_HOST: swarm.domain.com
    volumes:
      - bridge-data:/data
    ports:
      - "8080:8080"

volumes:
  bridge-data:
```

Notes:

- The container runs as uid/gid `65532` (distroless `nonroot`). A named volume
  mounted on `/data` gets the right ownership automatically; for a bind mount,
  make the host directory writable by uid 65532 (`chown 65532:65532 <dir>`).
- The image has no shell. Its `HEALTHCHECK` runs `swarm-horde-bridge -healthcheck`,
  which queries `http://127.0.0.1:$PORT/health`.
- To use a YAML file instead of (or in addition to) environment variables, mount
  it at `/etc/swarm-horde-bridge/config.yaml` (or set `CONFIG_FILE`).
- If `jobs.json` is corrupt at startup, it is renamed to
  `jobs.json.corrupt-<unix time>` (kept for inspection), an error is logged and
  the bridge starts with an empty store.

## Running from source

```bash
git clone https://github.com/Cubit-Studios/swarm-horde-bridge.git
cd swarm-horde-bridge
cp config.yaml.example config.yaml   # optional, environment variables also work
go build -o swarm-horde-bridge ./cmd/server
./swarm-horde-bridge -config config.yaml
```

Outside the container the job store defaults to `./data/jobs.json` (relative to
the working directory). `scripts/build.sh` / `scripts\build.bat` cross-compile a
Linux amd64 binary.

## Configuration

Settings are read from the optional YAML file (`-config` flag, default
`config.yaml`, or `CONFIG_FILE`), then overridden by environment variables.
If the config file does not exist, the bridge starts from environment variables
only. Invalid or missing required settings stop the service at startup with an
explicit error.

### Environment variables

| Variable              | YAML key               | Default                   | Description |
|-----------------------|------------------------|---------------------------|-------------|
| `HORDE_HOST`          | `horde.host`           | *(required)*              | Base URL used to call the Horde API, e.g. `https://horde.domain.com` or an internal `http://horde-server:5000` |
| `HORDE_API_KEY`       | `horde.api_key`        | *(required)*              | Horde service account token (sent as `Authorization: ServiceAccount <token>`) |
| `HORDE_TEMPLATE_ID`   | `horde.template_id`    | *(required)*              | Horde job template id used for preflights |
| `HORDE_STREAM_ID`     | `horde.stream_id`      | *(required)*              | Horde stream id used for preflights |
| `HORDE_PUBLIC_URL`    | `horde.public_url`     | value of `HORDE_HOST`     | Base URL of the job links posted to Swarm (`<url>/job/<id>`) |
| `HORDE_TIMEOUT`       | `horde.timeout`        | `30`                      | Horde API request timeout, seconds |
| `WEBHOOK_TOKEN`       | `server.webhook_token` | *(empty: no auth)*        | Shared secret required on the webhook, as `?token=<secret>` or header `X-Webhook-Token`; 401 otherwise. A warning is logged at startup when unset |
| `SWARM_ALLOWED_HOST`  | `swarm.allowed_host`   | *(empty: no check)*       | Host (optionally `host:port`) that `update_url` must point to, over https (http only for `localhost`/`127.0.0.1`); other URLs are rejected with 400. A warning is logged at startup when unset |
| `SWARM_TIMEOUT`       | `swarm.timeout`        | `30`                      | Timeout of status updates sent to Swarm, seconds |
| `PORT`                | `server.port`          | `8080`                    | HTTP listen port (also used by `-healthcheck`) |
| `MONITOR_INTERVAL`    | `monitor.interval`     | `30`                      | Horde job polling interval, seconds |
| `MAX_JOB_AGE`         | `monitor.max_job_age`  | `14400`                   | Seconds after which an unfinished job is reported to Swarm as failed and no longer tracked |
| `TIMEOUT_SHUTDOWN`    | `timeouts.shutdown`    | `5`                       | Graceful shutdown timeout, seconds |
| `RETRY_MAX_ATTEMPTS`  | `retry.max_attempts`   | `3`                       | Attempts per Horde API call (transport errors, HTTP 5xx/408/429 are retried; other 4xx are not) |
| `RETRY_INITIAL_DELAY` | `retry.initial_delay`  | `1`                       | Initial retry backoff, seconds (doubles each attempt) |
| `RETRY_MAX_DELAY`     | `retry.max_delay`      | `5`                       | Maximum retry backoff, seconds |
| `LOG_LEVEL`           | `log_level`            | `info`                    | `trace`, `debug`, `info`, `warn`, `error` |
| `DATA_DIR`            | `data_dir`             | `data` (relative to the working directory; the Docker image sets `/data`) | Directory of the persisted job store (`jobs.json`); created if missing |
| `CONFIG_FILE`         | -                      | `config.yaml` (`/etc/swarm-horde-bridge/config.yaml` in the image) | Default path of the optional YAML file; the `-config` flag overrides it |

Removed settings: `swarm.host` / `SWARM_HOST` and `timeouts.http_client` /
`TIMEOUT_HTTP_CLIENT` were never used and are now ignored if present. Swarm
updates always go to the update URL supplied by Swarm in each request.

### Swarm test definition

Create a test definition in Swarm that POSTs JSON to the bridge:

- URL: `https://<bridge-host>/webhook/swarm-test?token=<WEBHOOK_TOKEN>`
  (omit `?token=...` if `WEBHOOK_TOKEN` is not set)
- Body (JSON): `{"changelist": "{change}", "update_url": "{update}"}`

Swarm test definitions only let you customise the URL and body, so the token is
passed as the `token` query parameter. The bridge never logs query strings. The
`X-Webhook-Token` header is also accepted for other callers. Prefer https in
front of the bridge so the token is not sent in clear text.

## API Endpoints

- `GET /health` - Health check endpoint (not logged)
- `POST /webhook/swarm-test` - Swarm webhook endpoint (returns `202 Accepted` after validation; the job is created in the background)
- `GET /jobs` - List jobs currently tracked (change, Horde job id/link, status, reason). Swarm update URLs are not exposed.

## Troubleshooting

- `HTTP 401` from Horde: the service account token in `HORDE_API_KEY` is wrong or revoked.
- `HTTP 403` from Horde: the service account lacks `CreateJob` on the stream/template ACLs,
  or `ViewJob` to read jobs.
- Unexpected redirects from Horde usually mean `HORDE_HOST` has the wrong scheme/host or the
  token was not accepted (Horde redirects unauthenticated requests to its login page).
- `401` on the webhook: the `token` query parameter does not match `WEBHOOK_TOKEN`.
- `400 Invalid update_url` on the webhook: the update URL Swarm sent does not match
  `SWARM_ALLOWED_HOST` (check the host name Swarm uses for its own URLs, and https).

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
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o swarm-horde-bridge ./cmd/server

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
