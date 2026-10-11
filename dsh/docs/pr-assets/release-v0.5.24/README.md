# v0.5.24 release acceptance

[中文](README.zh-CN.md)

Verified on 2026-10-04 (Asia/Shanghai). The packages under test are `dsh-mnemon@0.5.24` and `dsh-mnemon-source-memory-spaces@0.5.16`, as `pnpm release:version` builds them from `main` `b2481b04`, which includes #332. The other 16 component packages come from npm at the Starter's pinned versions, which 0.5.24 does not change.

## Real official WebUI on both supported hosts

On unmodified npm DSH `0.2.0-rc.2` (npm `latest` and `next`) and `0.1.7-rc.2`, each with Node `24.19.0` and a fresh home, DSH home and pnpm store:

1. Started the host without Mnemon installed and opened **Plugins → Add plugin**.
2. Installed `dsh-mnemon`. DSH chose the install source itself; its speed check picked the mainland China mirror. A loopback registry answered with npm's real metadata plus the two new versions. The profile then held dsh-mnemon 0.5.24 and Memory Spaces 0.5.16, with every other component at its pinned npm version.
3. Clicked **Enable now**; the Memory System appeared without a host restart. [Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.24/status.png) reports **dsh-mnemon 0.5.24 / System nominal**, and Mnemon Native names **Mnemon 0.2.9**.
4. Added a Runtime memory entry and read it back after reloading the page: [Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.24/runtime.png).
5. Created and activated a Mnemon Native space in the WebUI: [Memory Spaces](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.24/spaces.png). Wrote a fact with Mnemon CLI `0.2.9`, recalled it with the CLI, then found the same fact with the WebUI's **Direct recall**: [Recall](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.24/recall.png).
6. Wrote three more memories with the CLI that carry the entity `Atlas`, and one that only mentions Atlas. On **Entities**, `Atlas` counts 3, and selecting it lists exactly those three ("Showing 3 / 3"). Related memories stay collapsed behind **Find related memories**, and no recall runs: [Entities](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.24/entities.png). Pressing it found the memory that only mentions Atlas, and the button became **Hide**: [Related memories](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.24/entities-related.png).
7. Opened **Check versions**. dsh-mnemon lists 0.5.24 as installed and names DSH's own plugin installer as its update route. Before publication npm's latest is still 0.5.23, so the dialog marks 0.5.24 as a local version and offers no update for it. The Mnemon CLI row offers its own npm update, since npm now has Mnemon 0.2.10. No restart notice appeared after **Enable now**, since the version that runs is the one installed.

Both hosts passed every step without a host warning or console error, and each host process was started once. [validation.json](validation.json) has the package digests and each host's results. Profiles and memories are synthetic.

![Entities on DSH 0.2.0-rc.2: Atlas counted 3 and listed 3, related memories collapsed](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.24/entities.png)

![Related memories found on demand](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.24/entities-related.png)

## The fix in this release

[Entities page counts and lists from every memory](../entities-complete-index/README.md) has the before-and-after runs on synthetic spaces on both hosts, the timings on 4 × 500 memories, and the counts from a copy of the export behind the report.

## Validation and release boundary

`pnpm run release:check` confirms the Starter 0.5.24 on the `latest` tag with Memory Spaces 0.5.16 pinned; publication computes the changed packages from the previous release. No plugin uses a new SDK export from the Starter, so no peer floor moves. The version bump changes two specifier lines in the lockfile, and pnpm 10.13.1, the CI version, accepts it with `--frozen-lockfile`.

The release pull request and the publication workflow run the complete workspace and packed-plugin verification. Publication then:
- freezes the merged main revision;
- publishes the changed packages and reads them back from npm;
- installs the complete 18-package combination and checks a real Registry upgrade;
- creates the GitHub release.

These screenshots show the versioned local packages; they do not by themselves certify npm publication.
