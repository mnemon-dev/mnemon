# Provider Lab

This localhost-only lab exercises every long-term Memory Provider supported by
`dsh-mnemon`. Private server deployments run in Docker; Holographic remains
in-process. Mnemon Native and ByteRover use their local CLI contracts.

## Safety boundary

- Every published port binds to `127.0.0.1`.
- The checked-in OpenViking key is deliberately a local demo credential.
- Provider state lives in named Docker volumes and is excluded from Memory
  Space packs.
- Ollama remains on the host and is reached from containers through
  `host.docker.internal`; no content needs to leave the machine.

## Upstream sources and ports

| Provider | Installation | DSH endpoint |
| --- | --- | --- |
| OpenViking | official GHCR image | `http://127.0.0.1:1933` |
| Honcho | official source Dockerfile + Postgres + Redis | `http://127.0.0.1:18000` |
| Mem0 | official self-host server Dockerfile + pgvector | `http://127.0.0.1:18888` |
| Hindsight | official GHCR standalone image | `http://127.0.0.1:18889` |
| RetainDB | official Local Dockerfile | `http://127.0.0.1:18990` |
| Supermemory | official signed release binary wrapped by Docker | `http://127.0.0.1:18787` |

Hindsight's optional control plane is at `http://127.0.0.1:19999`, and the
RetainDB Local viewer is at `http://127.0.0.1:18991`.

## Start

1. Clone the current upstream repositories into one directory with subfolders
   named `honcho`, `mem0`, and `retaindb`.
2. Copy `.env.example` to `.env` and set `PROVIDER_LAB_SOURCES` to that absolute
   directory.
3. Ensure Ollama has `qwen2.5:3b` and `nomic-embed-text`.
4. Run `docker compose up -d --build` from this directory.
5. Run `node scripts/seed-provider-lab.mjs` from the repository root after
   `pnpm run build && pnpm --workspace-concurrency=1 -r build`. Pass the Supermemory API key printed by its first-boot log
   as `SUPERMEMORY_API_KEY`.

Use `docker compose ps` and `node scripts/probe-provider-lab.mjs` for a concise
health report. The seed command uses public plugin composition and defaults to the isolated
`provider-lab/.state/memory` root, not personal memory. Use `MNEMON_DATA_DIR`
only for a disposable lab root; the command reconciles its Provider mappings.
It skips seeding a namespace that already has observable content. It reconciles the selected lab
connection settings on every run; set `PROVIDER_LAB_ONLY` to a comma-separated
provider list when only part of the lab should be checked.

Honcho's lab entrypoint applies migrations, reconciles its embedding model, and
pins the vector dimension to the 768-dimensional `nomic-embed-text` output
before starting the API. Mem0 uses the same local embedding dimension and the
configured Ollama chat model.

## Seeded WebUI fixture

The seed creates one active `Provider Lab · …` Memory Space for each of the nine
providers and writes five architecture, routing, UI-contract, privacy, and
ecosystem-compatibility facts. Extracting providers may expose more or fewer
provider-native units than the five source documents. In particular, Hindsight
can emit observations and graph links, while Supermemory Content merges its
extracted memories with still-browseable source documents.

Validate the fixture in the real DSH WebUI:

1. **Overview** distinguishes real graphs, disconnected content projections,
   and query-only providers.
2. **Recall** shows one source card per active searchable Memory Space and keeps
   provider attribution on every result.
3. **Content** marks ByteRover query-only and enumerates the other browseable
   providers without recall side effects.
4. **Entities** exposes only Mnemon Native, Hindsight, and Holographic as entity
   indexes; other providers remain explicitly unsupported.
