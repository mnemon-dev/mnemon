# Changelog

All notable changes to Mnemon will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed
- `Diff` now sorts matches by `Similarity` descending before selecting the overall
  suggestion. Previously, `KeywordSearch` ordered candidates by token overlap score,
  so a high-keyword-score candidate classified as ADD could mask a lower-keyword-score
  candidate with higher Jaccard similarity that should have been UPDATE or DUPLICATE.
- Deduplication false positives on scientific and domain-specific text:
  - Removed bare `"not"` from negation words — it appears in virtually all
    scientific prose and caused unrelated records to be classified as CONFLICT.
  - Gated negation-word check behind similarity ≥ 0.7 — at borderline
    similarity, shared domain vocabulary is not a reliable conflict signal.
  - Raised cosine dedup threshold from 0.70 to 0.85 — same-domain
    different-fact pairs (e.g. survey records at different locations) produce
    cosine ~0.75 with nomic-embed-text and were incorrectly triggering UPDATE.
  - Switched token dedup from bidirectional-max (`ContentSimilarity`) to
    Jaccard (`|A∩B|/|A∪B|`) — penalises texts that share vocabulary but
    differ in most tokens, preventing formulaic records from scoring as UPDATE.

## [0.1.4] - 2026-05-16

### Added
- `remember --entity-mode` to choose `merge`, `provided`, or `auto` entity handling.

### Fixed
- Store name validation for resolved store paths.
- Query limit validation for recall and related command paths.
- Sidecar migration error reporting.
- Setup integration cleanup reliability.

### Note
- Harness modules, harness documentation, and harness evaluation assets remain experimental and are not part of the v0.1.4 public stability guarantee.

## [0.1.3] - 2026-05-03

### Added
- `MNEMON_EMBED_DIMENSIONS` env var for Matryoshka dimension truncation (e.g., 256-dim instead of 768)

## [0.1.1] - 2026-02-22

### Added
- OpenClaw full integration: internal hook (`agent:bootstrap`) + plugin (`before_prompt_build`)
- `mnemon setup --target openclaw` now auto-deploys skill, hook, plugin, and config
- Optional hook selection for OpenClaw (remind, nudge, compact) matching Claude Code parity
- Plugin version patching from binary ldflags
- LLM-supervised tagline in README

### Changed
- README restructured: technical differentiator (comparison table) moved above value proposition
- OpenClaw setup no longer requires manual plugin configuration

## [0.1.0] - 2026-02-21

Initial public release.

### Added
- Core CRUD: `remember`, `recall`, `forget`, `search`, `status`, `log`
- Four-graph architecture: temporal, entity, causal, semantic edges
- Intent-aware smart recall with beam search graph traversal
- Built-in deduplication and conflict resolution
- Retention lifecycle: importance decay, access-count boosting, garbage collection
- Named memory stores for data isolation (`mnemon store list|create|set|remove`)
- `MNEMON_STORE` environment variable and `--store` CLI flag for store selection
- Automatic migration of legacy `~/.mnemon/mnemon.db` to `~/.mnemon/data/default/`
- Optional embedding support via Ollama (`nomic-embed-text`)
- Knowledge graph visualization (`mnemon viz`)
- Claude Code integration via hooks (prime, remind, nudge, compact)
- OpenClaw integration via skill deployment
- `mnemon setup` interactive installer with `--eject` support
- Release pipeline: GoReleaser, GitHub Actions, Homebrew tap
- Comprehensive documentation with Chinese translations

[Unreleased]: https://github.com/mnemon-dev/mnemon/compare/v0.1.4...HEAD
[0.1.4]: https://github.com/mnemon-dev/mnemon/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/mnemon-dev/mnemon/compare/v0.1.2...v0.1.3
[0.1.1]: https://github.com/mnemon-dev/mnemon/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/mnemon-dev/mnemon/releases/tag/v0.1.0
