# Roadmap

**English** | [简体中文](../zh-CN/roadmap.md) | [Documentation](./README.md)

This page separates shipped foundations from remaining work. Future items are directions, not release promises.

## Shipped foundations

- Independent Source, Strategy and Provider packages, a small public contribution service, Source-owned pages and one immutable View per executing turn.
- A Starter with the Layered strategy as its default, the General strategy as an alternative main strategy, and three optional enhancements that work with either.
- Source-local Provider descriptors, connection schemas, redaction and child-Fiber registration.
- Public-contract tests, independently installed artifacts, isolated Headless/WebUI fixtures and selective package releases.

See [Architecture](./development/architecture.md), [Development](./development/README.md) and [Release process](./development/releasing.md) for the actual scope and limits.

## Next: reliability and recoverability

- Persist background-review watermarks and pending activity across restarts; add bounded backoff and explicit retry.
- Improve idempotence and cancellation under interrupted maintenance; retain evidence of partial writes and source revisions.
- Add a deterministic sensitive-content defense before memory admission. Current model guidance is not a secret scanner.
- Expand real WebUI and fault-injection coverage: scopes, concurrent edits, capacity limits, remote deployment paths, provider recovery and supported OS combinations.
- Extend the [verified compatibility matrix](./reference/compatibility.md), including explicitly authorized real-Provider tests. Adapter fixtures alone are insufficient.

## Later: controlled maintenance and extension growth

- Reviewable long-term consolidation, conflict/decay candidates and deliberate forgetting.
- Stronger multi-component backup/restore drills and repair tools for damaged metadata or missing projections.
- Explicit data-format migration and rollback procedures when persistent formats actually need to change.
- Better background-review history and diagnostics; broader internationalization of Host errors and command output.
- A reusable DSH directory picker when the Host exposes the necessary capability; track [dsh-external/issues#603](https://github.com/dsh-external/issues/issues/603) (private repository; requires access, and unauthenticated requests return 404).
- Candidate Source/Strategy evaluation and controlled promotion for RSI: preserve inputs, artifacts, permissions and comparison results before an explicit installation decision.

## Boundaries that remain

The current UI presents memory behavior, not a generic plugin marketplace or View canvas. External plugins follow DSH installation and the public author contracts.

Cordis ownership is not a sandbox. There is no automatic execution of model-generated code, universal deletion guarantee, cross-Provider transaction, notification daemon or claim that every third-party memory plugin integrates without adaptation.
