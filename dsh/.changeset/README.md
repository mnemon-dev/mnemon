# Release intents

Every pull request that changes a published artifact adds a changeset with
`pnpm changeset`. Select only packages whose own artifact or published metadata
changes. Test-only, documentation-site and CI-only changes may use an empty
changeset when an explicit no-release record helps review.

Packages use independent versions. `dsh-mnemon` remains the aggregate Starter:
Changesets bumps it whenever an exact plugin dependency moves, and its published
manifest records the fully tested plugin composition. Run `pnpm release:version`
in a dedicated release pull request; it applies pending intents, synchronizes
the aggregate and fixture pins plus generated version metadata, and updates the
lockfile.

The npm workflow compares the frozen release revision with the preceding
release revision, packs only packages whose versions advanced, publishes plugin
dependency layers with bounded concurrency, and publishes the Starter last.
