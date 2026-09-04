# Contributing to Mnemon

Thank you for considering contributing to Mnemon!

## What We Welcome

- Bug fixes with a reproducing test or E2E scenario
- Performance improvements with benchmark evidence
- Documentation improvements (typos, clarity, missing examples)
- New integrations for LLM CLIs beyond Claude Code and OpenClaw

For significant features or architectural changes, please **open an issue first** to discuss the approach before writing code.

## Development Setup

```bash
git clone https://github.com/mnemon-dev/mnemon.git
cd mnemon
make build
```

**Optional**: Install [Ollama](https://ollama.ai) + `nomic-embed-text` for embedding-related development.

## Running Tests

```bash
make test              # Required deterministic CI suite for the Mnemon product
make test-integration  # Opt-in CLI E2E, timing, race, process, and Docker suite
make test-live         # Explicit paid Pi/DeepSeek evaluation
```

`make test` must pass before submitting a PR. Run `make test-integration`
proportionally when changing CLI E2E behavior or `mnemon agency` process, timing,
transport, or Docker boundaries; it is intentionally outside regular CI.

## Code Style

- Format with `gofmt` (the standard Go formatter)
- Follow [Effective Go](https://go.dev/doc/effective_go) conventions
- All exported functions and types must have doc comments
- Use `fmt.Errorf("context: %w", err)` for error wrapping

For architecture, concurrency, persistence, abstraction, and test design, follow the
[Go Engineering Standard](docs/development/go-engineering-standard.md). Design
patterns and Go language features are tools for reducing change amplification,
not quotas or substitutes for explicit safety checks.

## Commit Messages

We follow a lightweight Conventional Commits style:

```
feat: add intent override flag to recall command
fix: handle short IDs in link command
docs: document the missing usage flag
```

Use the type that reflects the primary project effect and write the summary in
imperative form. The CHANGELOG filter excludes `docs:`, `test:`, `ci:`, and
`chore:` commits from release notes.

## Submitting Changes

1. Fork the repository and create a feature branch from `master`.
2. Make your changes and run the proportional test level, including
   `make test-integration` for affected `mnemon agency` boundary behavior.
3. Update documentation (USAGE.md, DESIGN.md, or README) if your change affects user-facing behavior.
4. For user-facing changes, describe the release-note impact in the PR body. Maintainers update `CHANGELOG.md` during release preparation unless they explicitly ask for a changelog entry in the PR.
5. Open a pull request against `master`.

## Releasing

Releases are fully automated. Maintainers tag and push:

```bash
git tag v0.2.0
git push origin v0.2.0
```

This triggers GitHub Actions → runs tests → builds platform artifacts for the
single `mnemon` executable via GoReleaser → publishes a GitHub Release →
updates the Homebrew tap → verifies and publishes the npm platform artifacts →
publishes `@mnemon-dev/mnemon` last.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
