# Development and Deployment

## Local Development

Prerequisites:

- Go 1.24.6 or newer in the 1.24 series
- `make`
- `jq` for the E2E test script

Common commands:

```bash
make deps
make build
make unit
make test
make install
```

Use a project-local data directory when testing manually:

```bash
MNEMON_DATA_DIR=.mnemon-dev ./mnemon store create default
MNEMON_DATA_DIR=.mnemon-dev ./mnemon remember --no-diff "Local development memory" --cat fact --imp 3
MNEMON_DATA_DIR=.mnemon-dev ./mnemon recall "development memory"
```

## Container Development

Create a local environment file:

```bash
cp .env.example .env
```

Start a shell inside the Go development image:

```bash
make compose-dev
```

Inside the container:

```bash
make build
make test
```

## Container Deployment

Build the runtime image:

```bash
make docker-build
```

Run one command with persistent data mounted at `/mnemon`:

```bash
docker run --rm \
  -v mnemon-data:/mnemon \
  --env-file .env \
  mnemon-dev/mnemon:dev status
```

Or use Docker Compose:

```bash
cp .env.example .env
make compose-up
docker compose run --rm mnemon recall "query"
make compose-down
```

## Optional Embeddings

Mnemon works without embeddings. To use Ollama-backed vector search in the Compose environment:

```bash
docker compose --profile embeddings up -d ollama
docker compose exec ollama ollama pull nomic-embed-text
docker compose run --rm mnemon embed "hello"
```

The relevant environment variables are:

- `MNEMON_EMBED_ENDPOINT`
- `MNEMON_EMBED_MODEL`

For host-based Ollama, set `MNEMON_EMBED_ENDPOINT=http://host.docker.internal:11434` on Docker Desktop, or use the host gateway address for Linux deployments.

## Release Deployment

Tagged releases are handled by GoReleaser through `.github/workflows/release.yml`.

Required repository secret:

- `HOMEBREW_TAP_TOKEN`, only needed for publishing the Homebrew tap

Create a local snapshot build without publishing:

```bash
make release-snapshot
```
