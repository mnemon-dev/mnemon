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
make unit       # Go unit tests (go test ./...)
make test       # Full E2E test suite (scripts/e2e_test.sh)
make vet        # Static analysis (go vet ./...)
```

Both `make unit` and `make test` must pass before submitting a PR.

## Code Style

- Format with `gofmt` (the standard Go formatter)
- Follow [Effective Go](https://go.dev/doc/effective_go) conventions
- All exported functions and types must have doc comments
- Use `fmt.Errorf("context: %w", err)` for error wrapping

## Commit Messages

We follow a lightweight conventional commits style:

```
Add intent override flag to recall command
Fix panic in link command with short IDs
Update USAGE.md with missing recall flags
```

Prefix with a verb in imperative form. The CHANGELOG filter excludes `docs:`, `test:`, `ci:`, `chore:` prefixed commits from release notes.

## Submitting Changes

1. Fork the repository and create a feature branch from `master`.
2. Make your changes and ensure `make unit` and `make test` pass.
3. Update documentation (USAGE.md, DESIGN.md, or README) if your change affects user-facing behavior.
4. For user-facing changes, describe the release-note impact in the PR body. Maintainers update `CHANGELOG.md` during release preparation unless they explicitly ask for a changelog entry in the PR.
5. Open a pull request against `master`.

## Releasing

Releases are fully automated. Maintainers tag and push:

```bash
git tag v0.2.0
git push origin v0.2.0
```

This triggers GitHub Actions → runs tests → builds cross-platform binaries via GoReleaser → publishes a GitHub Release → updates the Homebrew tap.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
