# Changelog

All notable changes to Mnemon will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `mnemon recall --brief` and `mnemon search --brief` now provide a bounded,
  unindented JSON discovery projection. `--excerpt-chars` controls the per-item
  excerpt limit, and `mnemon show <id>` retrieves one selected insight in full.
- `mnemon setup --target zcode` now installs a ZCode-compatible Mnemon skill.
  With `--global`, setup also registers user-level `SessionStart`,
  `UserPromptSubmit`, and `Stop` process hooks in
  `~/.zcode/cli/config.json`; project-local setup intentionally skips hooks
  because ZCode does not execute project-level hook configuration. Hook assets
  use Bash on macOS/Linux and native Windows PowerShell on Windows.
- `mnemon setup --eject --target zcode` removes Mnemon-owned ZCode files and
  hook entries while preserving unrelated skills, hook events, and settings.
- `mnemon setup --target minimax-code` now installs a MiniMax Code skill to
  `.minimax/skills/mnemon/SKILL.md`, or to
  `~/.minimax/skills/mnemon/SKILL.md` with `--global`.
- MiniMax Code detection recognizes both the current `~/.minimax/` data
  directory and the legacy `~/.mavis/` directory migrated by current releases.
- `mnemon setup --eject --target minimax-code` removes only the Mnemon skill
  while preserving other MiniMax Code skills.

### Tests

- Added unit and CLI end-to-end coverage for Unicode-safe excerpt bounds,
  compact encoding, basic and smart recall, search, full-result lookup, and
  incompatible output flags.
- Added ZCode coverage for embedded artifacts, POSIX and Windows hook
  registration, unrelated configuration preservation, and scoped eject
  cleanup.
- Added MiniMax Code coverage for skill installation, current and legacy data
  directory detection, and scoped eject cleanup.

### Note

- The integration intentionally stays skill-only. MiniMax Code 3.0.65 ships a
  hook registry, but its current local Agent V2 turn path does not dispatch the
  user-prompt lifecycle event needed for reliable automatic recall.

## [0.2.7] - 2026-09-01

### Fixed

- `--readonly recall` now resolves and encodes SQLite file URI paths correctly
  on Windows and when `--data-dir` is relative to the current directory. This
  fixes the misleading `SQL logic error: out of memory (1)` failure reported
  in #123.
- Readonly queries retain `mode=ro` and `immutable=1`: they reject database
  writes, preserve recall counters and oplog, and create no WAL/SHM sidecars.

### Tests

- Added real SQLite and CLI regressions for absolute, relative, and Windows
  drive-relative paths, including spaces, Unicode, `#`, and `%`.
- Native Windows CI now runs the Memory command and storage tests in addition
  to the product build and command-boundary tests.

## [0.1.15] - 2026-06-18

### Added

- `mnemon setup --target cursor` now installs a Cursor integration, including a
  Mnemon skill, prompt files, and Cursor lifecycle hooks under `.cursor/` or
  `~/.cursor/`. The session-start hook primes Cursor with Mnemon guidance and
  memory status, the stop hook can nudge for durable-memory writeback, and an
  optional pre-compaction hook can remind users to preserve durable context.
- `mnemon setup --eject --target cursor` removes the installed Cursor skill and
  Mnemon hook entries while preserving unrelated Cursor hook configuration.
- Cursor environment detection is included in interactive setup when the
  `cursor` binary or `~/.cursor/` config directory is present.

### Fixed

- CI workflow permissions are now declared explicitly for the code-scanning
  workflow surface.

### Tests

- Added Cursor setup coverage for skill installation, executable hook writes,
  hook registration, preservation of unrelated Cursor hooks, and eject cleanup.

### Note

- Harness modules remain experimental and are not part of the v0.1.15 public
  stability guarantee. This release includes substantial internal harness
  iteration, documentation, and validation work, but release-path commands such
  as `mnemon setup` continue to keep formal integrations under `cmd/` and
  `internal/`.

## [0.1.14] - 2026-06-08

### Added

- Entity extraction now has an index-aware fourth path for known entities. When
  Mnemon has already seen an entity through provided metadata or earlier
  extraction, later graph operations can admit wider candidate tokens that match
  the known-entity index. This improves recall for personal project names,
  internal codenames, and single-segment CamelCase entities without globally
  loosening first-mention extraction.
- Store support for loading known entities from active insights, used by the
  graph engine to seed index-aware extraction while still skipping soft-deleted
  records.

### Changed

- Embedding blobs are now stored as little-endian `float32` values instead of
  `float64`, cutting embedding storage size roughly in half while keeping the
  public vector API and cosine-similarity calculations in `float64`.
- Existing official databases are migrated on open from legacy `float64`
  embedding blobs to the new `float32` storage format. The migration records its
  completion in SQLite `PRAGMA user_version` and leaves malformed or
  non-legacy-looking blobs untouched rather than blocking database startup.

### Fixed

- Claude Code project-local setup now detects the degenerate `$HOME/.claude`
  collision. When `mnemon setup` is run from `$HOME`, the apparent local
  `.claude/` directory is actually Claude Code's user-global config directory;
  Mnemon now writes absolute hook commands in that case so hooks resolve
  correctly from every future Claude Code session directory.
- Added symlink-aware and `CLAUDE_CONFIG_DIR`-aware coverage for Claude Code
  config collision detection, plus regression tests that genuine project-local
  installs keep relative hook paths.

### Tests

- Added round-trip and invalid-length coverage for `float32` vector
  serialization, plus legacy `float64` deserialization coverage for migration.
- Added store migration coverage for normal legacy embeddings, malformed blobs,
  and non-legacy-looking blobs that should be skipped.
- Added entity extraction and store tests for the known-entity path, including
  propagation of previously seeded project names without admitting arbitrary new
  wide tokens.

## [0.1.11] - 2026-05-27

### Added

- English/Chinese import guide coverage and setup skill guidance for using
  `mnemon import` on historical chat exports.

## [0.1.10] - 2026-05-27

### Added

- `mnemon import <file>` bulk-imports memory draft JSON files into a selected
  Mnemon store. Imported insights use the normal write path for duplicate and
  conflict detection, embeddings when Ollama is available, entity/causal/
  semantic graph construction, lifecycle scoring, and capacity pruning.
- `internal/importdraft` defines the public draft schema for historical chat
  imports:
  - top-level `schema_version`, `source`, `insights`, and optional `edges`
  - insight fields for `content`, `category`, `importance`, `tags`,
    `entities`, `source`, and RFC 3339 `created_at`
  - explicit edge fields for `source_index`, `target_index`, `edge_type`,
    `weight`, and `reason`
- `mnemon import --dry-run` validates draft structure without writing to the
  database, and `mnemon import --no-diff` inserts all draft insights without
  duplicate/conflict detection.
- [Import documentation](docs/IMPORT.md) with the full schema reference,
  category and importance guidance, edge-type guidance, examples, operational
  notes, and a copy-paste LLM prompt for turning chat exports into
  `memory_draft.json`.

### Fixed

- Historical imports no longer create incorrect temporal backbone edges when
  `created_at` is older than existing store contents. The import path disables
  real-time temporal edge generation, then repairs affected source timelines
  after all backdated insights are written so imported memories are inserted
  into the global chronological chain.
- Explicit draft edges now participate in effective-importance calculation
  before `AutoPrune` runs. Import finalization inserts explicit edges, repairs
  temporal edges, refreshes touched insight scores, and only then applies
  pruning.

### Changed

- The graph engine now supports internal `EngineOptions` with `TemporalMode`,
  allowing import to skip real-time temporal generation without changing the
  behavior of `mnemon remember`.
- Store internals gained helpers for source-ordered active insight scans and
  typed single-edge deletion, used by temporal repair during imports.

### Tests

- Added validation coverage for the import draft schema, including required
  content, category and importance constraints, RFC 3339 timestamps, edge index
  bounds, and source fallback behavior.
- Added command integration coverage for backdated import temporal repair and
  effective-importance refresh after explicit draft edges.

## [0.1.9] - 2026-05-26

### Changed

- **BREAKING**: `recall` now emits a compact, LLM-friendly JSON shape by
  default. The default result entries keep `id`, `content`, `category`,
  `importance`, `intent`, `matched_via`, `confidence`, and rounded `score`,
  while omitting verbose debug fields such as `signals`, timestamps,
  `access_count`, tags, entities, source, and traversal metadata. (#3)
- The previous full `recall` payload remains available with `--verbose` for
  scripts, debugging, and callers that still need `meta`, `signals`, or the
  full embedded insight object.

### Added

- `recall --verbose` flag to restore the previous full recall response.

## [0.1.8] - 2026-05-24

### Added
- `--embed-model` global flag exposes the existing `MNEMON_EMBED_MODEL` env
  var on the CLI, applied consistently across `embed`, `recall`, and
  `remember`. Useful for switching to multilingual models such as
  `nomic-embed-text-v2-moe:latest` on code-switched corpora without exporting
  the env var. Note: switching to a model with a different output dimension
  silently invalidates existing embeddings — backfill after changing.

## [0.1.7] - 2026-05-23

### Added
- Privacy-safe memory receipts via `mnemon receipt`, allowing users to show
  memory activity with identifiers, timestamps, summaries, and relation metadata
  without exposing stored memory content.

### Fixed
- Read-only store operations now skip access-count and oplog writes, preventing
  recall/search/status-style paths from attempting mutations when the store is
  opened in read-only mode.

### Changed
- Project licensing switched to Apache-2.0.

## [0.1.6] - 2026-05-18

### Added
- Codex integration: `mnemon setup --target codex` deploys the mnemon skill,
  prompt files, and Codex lifecycle hooks (`SessionStart`, `UserPromptSubmit`,
  `Stop`) into `.codex/` or `~/.codex/`. `mnemon setup --eject --target codex`
  removes the installed Codex surface while preserving unrelated hooks.
- Nanobot integration: `mnemon setup --target nanobot` deploys a skill file to
  `.nanobot/skills/mnemon/SKILL.md` (local) or `~/.nanobot/workspace/skills/mnemon/SKILL.md`
  (global, recommended). `mnemon setup --eject` removes it. Detection is automatic
  when the `nanobot` binary or `~/.nanobot/workspace/` directory is present.

### Note
- Harness modules remain experimental and are not part of the v0.1.6 public
  stability guarantee. Release-path integrations are implemented under `cmd/`
  and `internal/`.

## [0.1.5] - 2026-05-17

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

[Unreleased]: https://github.com/mnemon-dev/mnemon/compare/v0.1.15...HEAD
[0.1.15]: https://github.com/mnemon-dev/mnemon/compare/v0.1.14...v0.1.15
[0.1.14]: https://github.com/mnemon-dev/mnemon/compare/v0.1.13...v0.1.14
[0.1.11]: https://github.com/mnemon-dev/mnemon/compare/v0.1.10...v0.1.11
[0.1.10]: https://github.com/mnemon-dev/mnemon/compare/v0.1.9...v0.1.10
[0.1.9]: https://github.com/mnemon-dev/mnemon/compare/v0.1.8...v0.1.9
[0.1.8]: https://github.com/mnemon-dev/mnemon/compare/v0.1.7...v0.1.8
[0.1.7]: https://github.com/mnemon-dev/mnemon/compare/v0.1.6...v0.1.7
[0.1.6]: https://github.com/mnemon-dev/mnemon/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/mnemon-dev/mnemon/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/mnemon-dev/mnemon/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/mnemon-dev/mnemon/compare/v0.1.2...v0.1.3
[0.1.1]: https://github.com/mnemon-dev/mnemon/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/mnemon-dev/mnemon/releases/tag/v0.1.0
