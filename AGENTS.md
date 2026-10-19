# Mnemon Agent Guidelines

## Development

- Build the single product executable with `go build -o mnemon .`.
- Run `make test` for the deterministic CI suite. It excludes real daemon
  readiness, CLI E2E, wall-clock scenarios, Docker, and provider calls.
- Run `make test-integration` explicitly for CLI E2E plus Agency
  timing, race, process, and Docker boundaries; it is not a regular CI gate.
- Run `make test-live` only when explicitly validating the paid Pi/DeepSeek
  scenarios.
- Treat `cmd/memory` and `cmd/agency` as command namespaces within the single
  `mnemon` executable. Keep existing Memory commands at the root; expose Agency
  through `mnemon agency ...`. `mnemond` remains the local daemon/protocol role,
  not a second executable.
- Keep mnemond boundary suites under `test/mnemond` and their data-only fixtures
  under `testdata/mnemond`.
- Treat `.claude/`, `.codex/`, `.openclaw/`, and similar host directories as
  local projection surfaces, not canonical project state.

## Go Engineering

- Read and follow [the Go engineering standard](docs/development/go-engineering-standard.md)
  before changing Go architecture, concurrency, durable state, or shared
  infrastructure.
- Use patterns, channels, generics, callbacks, and registries only when they
  reduce sources of truth, state combinations, or modification points. They are
  not usage quotas, and reducing `if` statements or total lines is not a goal.
- Keep authority, digest, fence, bounds, CAS cardinality, and fail-closed checks
  explicit. Every goroutine must have an owner, cancellation, bounded work, and
  a wait path.
- Preserve independent replay, crash, authorization, and race oracles while
  compressing fixtures and shared setup.

## DeepSeek Harness Workspace

- `dsh/` is the DeepSeek Harness plugin workspace: TypeScript, pnpm, MIT. It is
  not part of the Go module; `dsh/go.mod` keeps it out of the module zip, and
  Go code does not belong there.
- Work inside `dsh/` follows `dsh/CONTRIBUTING.md`: install with
  `pnpm install --frozen-lockfile`, verify with `pnpm run verify`, and keep its
  bilingual documentation and changeset rules.
- `.github/workflows/dsh-ci.yml` runs the workspace's CI from `dsh/`. Files in
  `dsh/.github/` are inactive until the workspace's release lane and repository
  rules move to the root.

## Commit Discipline

- Prefer small, logical commits. Split unrelated work instead of committing a
  broad mixed diff.
- Keep tightly coupled changes together when splitting would leave either commit
  misleading or incomplete.
- Use the project style already present in history: a concise Conventional
  Commit title plus one or two focused body paragraphs, with bullets only when
  they improve scanning.
- Choose the commit type by the primary project effect:
  - `feat` for new developer-facing or Agency capabilities.
  - `fix` for correctness repairs.
  - `test` for tests, eval scenarios, or fixtures that do not add a new
    reusable capability.
  - `docs` for documentation-only changes.
  - `refactor` for structure changes without intended behavior changes.
  - `chore` for repository hygiene and maintenance.
- Mention validation in the body when tests, evals, or manual checks are part of
  the work.
- Do not include agent attribution or co-author lines unless explicitly asked.
