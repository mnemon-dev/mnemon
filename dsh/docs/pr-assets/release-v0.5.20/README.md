# v0.5.20 release acceptance

[中文](README.zh-CN.md)

Verified on 2026-09-29 (Asia/Shanghai). The package under test is `dsh-mnemon@0.5.20` as `pnpm release:version` builds it; the other 17 component packages come from npm at the Starter's pinned versions, which 0.5.20 does not change. It was built from `94d1e84f` (#313 and #314). The release pull request builds the same package from `4ea87e5b`, because #315 changed documentation only: both artifacts have SHA-256 `c446c553…`.

## Real official WebUI on both supported hosts

On unmodified npm DSH `0.2.0-rc.1` and `0.1.7-rc.2`, each with Node `24.19.0` and a fresh home, DSH home and pnpm store:

1. Started the host without Mnemon installed and opened **Plugins → Add plugin**.
2. Installed `dsh-mnemon`. DSH chose the install source itself (its speed check picked the mainland China mirror). A loopback registry answered with npm's real metadata, read as npm will a day after the release, plus 0.5.20. Installation took 21.8 s on 0.2.0-rc.1 and 13.2 s on 0.1.7-rc.2, starting from an empty pnpm store.
3. Clicked **Enable now**; the Memory System appeared within about a second, without a host restart. [Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.20/status.png) reports **dsh-mnemon 0.5.20 / System nominal**, and Mnemon Native names **Mnemon 0.2.7**.
4. Added a Runtime memory entry and read it back after reloading the page: [Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.20/runtime.png).
5. Created and activated a Mnemon Native space in the WebUI: [Memory Spaces](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.20/spaces.png). Wrote a fact with Mnemon CLI `0.2.7`, recalled it with the CLI, then found the same fact with the WebUI's **Direct recall**: [Recall](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.20/recall.png).

Both hosts passed every step without a host warning or console error, and each host process was started once. [validation.json](validation.json) has the package digest and each host's results. Profiles and memories are synthetic.

![Status on DSH 0.2.0-rc.1](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.20/status.png)

![The CLI-written Native memory found by Direct recall](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.20/recall.png)

## Upgrades and other ways in

The [cold start and upgrade record](../starter-group-upgrade/README.md) checks the same package through the command line, Headless and the desktop layout. It also checks updates from 0.5.16 through 0.5.19, including a profile stuck with the Starter row turned off, and pnpm's release-day fallback. The [component group record](../starter-group-readiness/README.md) shows the Plugins page before and after the change.

## Validation and release boundary

`pnpm run release:check` selects only the Starter for publication; no component package changes. No plugin uses a new SDK export, so no peer floor moves, and the lockfile is unchanged. The release pull request and the publication workflow run the complete workspace and packed-plugin verification. Publication then freezes the merged main revision, publishes the Starter and reads it back from npm. It installs the complete 18-package combination and checks a real Registry upgrade before creating the GitHub release. These screenshots show the versioned local package; they do not by themselves certify npm publication.
