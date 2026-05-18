# Mnemon Agent Guidelines

## Development

- Build with `go build -o mnemon .`.
- Run the E2E suite with `bash scripts/e2e_test.sh` or `make test`.
- Validate harness module manifests with `make harness-validate` when changing
  harness module assets.
- Treat `harness/` as an experimental, not-yet-released harness layer. Do not
  use it as an implementation dependency for release-path commands such as
  `mnemon setup`; formal integrations belong under `cmd/` and `internal/`.
- Treat `.claude/`, `.codex/`, `.openclaw/`, and similar host directories as
  local projection surfaces, not canonical project state.

## Commit Discipline

- Prefer small, logical commits. Split unrelated work instead of committing a
  broad mixed diff.
- Keep tightly coupled changes together when splitting would leave either commit
  misleading or incomplete.
- Use the project style already present in history: a concise Conventional
  Commit title plus one or two focused body paragraphs, with bullets only when
  they improve scanning.
- Choose the commit type by the primary project effect:
  - `feat` for new developer-facing or harness capabilities.
  - `fix` for correctness repairs.
  - `test` for tests, eval scenarios, or fixtures that do not add a new
    reusable capability.
  - `docs` for documentation-only changes.
  - `refactor` for structure changes without intended behavior changes.
  - `chore` for repository hygiene and maintenance.
- Mention validation in the body when tests, evals, or manual checks are part of
  the work.
- Do not include agent attribution or co-author lines unless explicitly asked.
