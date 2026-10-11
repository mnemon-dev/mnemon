# Memory space terminology

## Goal

Use **记忆空间** in Chinese and **memory space** in English for the persistent, Provider-backed unit. Keep individual memories, Runtime memory, Documents, Provider namespaces, and the overall memory system distinct.

## Plan

1. Update product copy, accessibility labels, settings, Agent guidance, command output, package documentation, and contributor templates. Review sentences that previously mixed the two terms instead of only replacing words.
2. Use `MemorySpace` for the canonical code model and related types, registry, internal helpers, UI components, and presentation keys. Keep deprecated type aliases where published consumers need them.
3. Preserve published v0.5.x tool/RPC identifiers, request/result fields, Provider adapter context, and persisted registry/Document/Pack formats. Document these compatibility spellings; a terminology change must not change routing, ownership, authorization, or data locations.
4. Preserve historical screenshot pixels and measured evidence. Capture current Chinese and English UI evidence with the new terminology, check narrow layouts, and update the current showcase where it displays the renamed concept.
5. Verify canonical/legacy type compatibility, existing data and Provider integration, UI behavior, bilingual documentation links, complete workspace checks, and independent package artifacts. Add changesets for every changed published package.

## Acceptance

- Current UI and authored product descriptions consistently use the new terms, including empty/error states and accessible names.
- Canonical code types and internal names express memory spaces; compatibility names are explicitly documented rather than silently removed.
- Existing configuration, registries, Document lineage, Packs, tools, and Provider adapters continue to work.
- Chinese/English flows remain usable at desktop and mobile widths, with current screenshots and validation limits recorded.

## Completed implementation

- Updated current product text, accessibility labels, Agent guidance, command output, bilingual documentation, package READMEs, contributor templates, and editable diagrams. Current README and guide images now use the terminology update; historical recordings and measured evidence remain identified as historical.
- Introduced canonical `MemorySpace` contracts, `MemorySpaceRegistry`, and service operations such as `spaceDirectory`, `createSpace`, and `mergeSpaces`. Existing published types and operations retain compatibility aliases. Provider factories accept the canonical authority name while supporting older hosts; the legacy protected HTTP Provider field remains a field so existing subclasses can still override it.
- Preserved registry file names and JSON shapes, tool/RPC spellings, Document/Pack lineage, revision encoding, CSS class maps, and historical documentation anchors. Migration fixtures verify that user-authored legacy titles are preserved.
- Fixed narrow directory controls and paths, wrapped Provider metadata badges and the header storage badge, and tightened navigation spacing for longer labels. [Bilingual browser evidence](../assets/memory-space-terminology/README.md) covers desktop and narrow layouts.
- Added patch release intent for all 17 changed published packages; no package version was changed or published by this terminology task.

## Validation

- Full `pnpm run verify`: passed, including deterministic builds, workspace typechecks, 783 root tests, 300 plugin tests, Headless activation, public entries, and package lint. Two opt-in native integration cases were skipped.
- After preserving the legacy fields as fields, Source build, root typecheck, and all 62 service tests passed again.
- Independent artifact verification: all 16 plugin repositories and 17 tarballs passed standalone installation, build, typecheck, tests, external public-SDK consumption, and real DSH activation. The external fixture compiles both model names and overrides the old protected Provider field.
- Changeset coverage checked against both tracked working-tree changes and newly added files: all 17 changed published packages are covered.
- Real browser checks: Chinese and English creation, editing, activation, content browsing, Runtime and Document flows, settings, language switching, and cancellation. At 390 × 844, the canvas and directory have no horizontal overflow; create-dialog buttons end at y=834. Desktop captures use 1365 × 1000. All 18 original JPEG captures have verified hashes.
- Current locale catalogs contain no old product term. Remaining legacy wording is limited to compatibility migration fixtures, historical raw measurements, and old URL anchors. Existing user data does not require migration.

Browser data was disposable and removed. Model quality, cloud Provider behavior, and the opt-in platform integrations are outside this verification record.
