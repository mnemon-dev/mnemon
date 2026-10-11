# Status version maintenance

## Plan

- Model installed, missing, unreadable, outdated, linked, and restart-pending states separately. Registry failures must not hide local installation guidance.
- Recommend the official `@mnemon-dev/mnemon` npm package (Node.js 22+). Detect its launcher and owning npm installation before offering an update; keep existing Homebrew and Go updates. Installation and migration are explicit terminal commands, with PATH and configured CLI-path guidance.
- Keep the two product cards compact. Expand dsh-mnemon to inspect Sources, Strategies, and Providers, their installed/recommended versions, and maintenance ownership.
- Update independently installed official subpackages only in their owning DSH Profile. Starter dependencies retain the Starter's tested pins and update with the Starter. Never replace source links or install arbitrary package names from RPC input.
- Serialize package writes, verify resulting versions, retain restart reminders across checks, and preserve management authorization.
- Verify Host state transitions, registry failures, ownership and update scope; exercise authorized/read-only UI, disclosure, copy feedback, and responsive rendering. Synchronize installation and operations documentation in English and Chinese.

## Acceptance

- Missing or unreadable CLI versions never appear as current; unavailable registry data still leaves npm instructions accessible.
- npm detection follows the executable used by DSH and does not update a different Node/npm installation.
- dsh-mnemon subpackages are collapsed initially and individually actionable only when independently managed by the active Profile.
- A successful package installation remains marked as requiring a Host restart until the Host restarts.
- Checks are read-only. Updates keep the existing management-authority boundary and reject unrecognized targets.

## Verification

Implemented and verified on 2026-09-06.

- The version dialog now distinguishes actionable installation states, keeps npm commands visible, and collapses post-install instructions and the sixteen-package inventory.
- Native CLI discovery belongs to Memory Spaces' public `native-cli` entry and is shared by the Host version checker and the Source runner. This avoids requiring a newer Core SDK when the Source is upgraded independently.
- Full `pnpm@10.13.1 run verify` passed, including 783 root tests, workspace tests, deterministic builds, Headless activation and package checks.
- Independent package verification passed for sixteen plugin repositories and seventeen packed artifacts.
- The real DSH WebUI was checked at 1280 × 720 and 390 × 844. Copying, disclosure, scrolling, and long package names work without horizontal overflow.
- [Screenshots and validation limits](../pr-assets/status-version-maintenance/README.md) record the real UI evidence and the simulated Windows coverage.
