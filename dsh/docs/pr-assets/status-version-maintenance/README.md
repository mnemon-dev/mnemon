# Status version maintenance evidence

Captured on 2026-09-06 from the real DSH 0.1.2-rc.1 WebUI, launched with `scripts/serve-e2e.mjs`. The disposable Profile contains a linked Starter and sixteen official subpackages. Memory directories are empty fixtures; no model requests or production data were used. The source location is a temporary alias. The Host's existing Homebrew CLI was inspected with `--version`; no machine-wide install or update was executed.

- [Desktop, collapsed](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/status-version-maintenance/desktop-collapsed.png): npm migration command, successful copy feedback, expandable post-install steps, and the subpackage toggle at 1280 × 720.
- [Desktop, expanded](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/status-version-maintenance/desktop-expanded.png): Source and Strategy versions, Starter pins, and maintenance ownership.
- [Mobile, expanded](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/status-version-maintenance/mobile-expanded.png): 390 × 844 bottom sheet with wrapping package names and a fixed footer. The dialog and all sixteen package rows have no horizontal overflow.

Validation:

- `pnpm@10.13.1 run verify` passed: bilingual documentation links, types, deterministic builds, all workspace tests, 783 root tests, real Headless activation, package contents and public-entry validation, publint and attw.
- `pnpm@10.13.1 exec node scripts/verify-plugin-artifacts.mjs --skip-build` passed for sixteen independent plugin repositories and seventeen packed artifacts, including real DSH installation of the packed Starter.
- Host regressions exercise npm ownership, Windows launcher invocation, missing/broken installations, exact Profile updates, preserved Starter pins and links, registry failures, concurrent update rejection, and post-update version verification.
- Client regressions exercise missing/offline states, npm guidance and copying, read-only access, update failures, duplicate clicks, package disclosure, and restart reminders.
- The existing opt-in native integration and Windows smoke tests remain skipped on this macOS run. Windows command discovery and invocation are covered by simulated filesystem/process fixtures, not a Windows machine.

The presentation baseline records the intentional version-maintenance changes separately from the original Source presentation migration baseline.
