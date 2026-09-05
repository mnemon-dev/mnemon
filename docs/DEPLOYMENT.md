# Development and Deployment

## Local Development

Prerequisites:

- Go 1.24.6 or newer in the 1.24 series
- Node.js 22 or newer for npm package tests
- `make`
- `jq` only when running the opt-in CLI E2E/integration suite

Common commands:

```bash
make deps
make build
make test              # deterministic CI suite
make test-integration  # optional E2E/process/Docker suite
make install
```

Use a project-local data directory when testing manually:

```bash
MNEMON_DATA_DIR=.mnemon-dev ./bin/mnemon store create default
MNEMON_DATA_DIR=.mnemon-dev ./bin/mnemon remember --no-diff "Local development memory" --cat fact --imp 3
MNEMON_DATA_DIR=.mnemon-dev ./bin/mnemon recall "development memory"
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

Mnemon works without embeddings. The Compose embeddings profile provides the
default Ollama-backed vector search setup:

```bash
docker compose --profile embeddings up -d ollama
docker compose exec ollama ollama pull nomic-embed-text
docker compose run --rm mnemon embed "hello"
```

The relevant environment variables are:

- `MNEMON_EMBED_ENDPOINT`
- `MNEMON_EMBED_MODEL`
- `MNEMON_EMBED_PROTOCOL`
- `MNEMON_EMBED_API_KEY`
- `MNEMON_EMBED_DIMENSIONS`

For host-based Ollama, set `MNEMON_EMBED_ENDPOINT=http://host.docker.internal:11434` on Docker Desktop, or use the host gateway address for Linux deployments.

An external OpenAI-compatible server can be selected with an endpoint ending
in `/v1`, for example `MNEMON_EMBED_ENDPOINT=http://host.docker.internal:18000/v1`.
Set `MNEMON_EMBED_MODEL` to a model exposed by that server and
`MNEMON_EMBED_API_KEY` when authentication is required. Use HTTPS whenever the
server is not on a trusted local network.

## Release Deployment

Tagged releases are handled through `.github/workflows/release.yml`. GoReleaser
publishes the native GitHub artifacts and Homebrew cask first. A dependent job
then stages those exact binaries as npm artifacts, verifies the host launcher,
and publishes the canonical `@mnemon-dev/mnemon` package.

Long-lived repository secret:

- `HOMEBREW_TAP_TOKEN`, only needed for publishing the Homebrew tap

Before the first npm release, reserve the `@mnemon-dev` npm scope and add a
granular `NPM_TOKEN` repository secret that can bootstrap the public
`@mnemon-dev/mnemon` package. After that first tagged release:

1. Configure `mnemon-dev/mnemon` and `release.yml` as the package's GitHub
   Actions [trusted publisher](https://docs.npmjs.com/trusted-publishers/) on
   npm, allowing direct `npm publish`.
2. Run the next tagged release and confirm that npm records GitHub Actions as
   its trusted publisher.
3. Delete the `NPM_TOKEN` repository secret and revoke the bootstrap token.

The npm release job uses Node.js 24 and requests only the OIDC permission needed
for token-free trusted publishing. npm automatically binds each publish to the
workflow and records provenance; `--provenance` also covers the one-time token
bootstrap release.

One tag is the only version source. For `v0.3.0`, the workflow publishes six
platform versions such as `0.3.0-darwin-arm64`, then publishes the `0.3.0` CLI
meta-package last. The meta-package pins each platform version through npm
aliases. Stable tags advance `latest`; prerelease tags advance `next`. Publishing
the meta-package last prevents npm users from observing an incomplete release.

Publishing is retry-safe: already published immutable versions are skipped, so
the failed `npm-release` job can be rerun without rebuilding or republishing the
GitHub release.

Create a local snapshot build without publishing:

```bash
make release-snapshot
```

The npm staging tools consume GoReleaser's `dist/artifacts.json`; they do not
maintain a second build matrix. They are exercised independently with:

```bash
npm test --prefix npm/cli
```
