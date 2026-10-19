# v0.5.22 release acceptance

[中文](README.zh-CN.md)

Verified on 2026-10-03 (Asia/Shanghai). The package under test is `dsh-mnemon@0.5.22`, as `pnpm release:version` builds it from `main` `a69d1a51`, which includes #325, #326 and #328. The 17 component packages come from npm at the Starter's pinned versions, which 0.5.22 does not change.

## Real official WebUI on both supported hosts

On unmodified npm DSH `0.2.0-rc.2` (npm `latest` and `next`) and `0.1.7-rc.2`, each with Node `24.19.0` and a fresh home, DSH home and pnpm store:

1. Started the host without Mnemon installed and opened **Plugins → Add plugin**.
2. Installed `dsh-mnemon`. DSH chose the install source itself; its speed check picked the mainland China mirror. A loopback registry answered with npm's real metadata plus the new version. The profile then held dsh-mnemon 0.5.22 with every component at its pinned npm version.
3. Clicked **Enable now**; the Memory System appeared without a host restart. [Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.22/status.png) reports **dsh-mnemon 0.5.22 / System nominal**, and Mnemon Native names **Mnemon 0.2.9**.
4. Added a Runtime memory entry and read it back after reloading the page: [Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.22/runtime.png).
5. Created and activated a Mnemon Native space in the WebUI: [Memory Spaces](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.22/spaces.png). Wrote a fact with Mnemon CLI `0.2.9`, recalled it with the CLI, then found the same fact with the WebUI's **Direct recall**: [Recall](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.22/recall.png).
6. Opened **Check versions**. dsh-mnemon lists 0.5.22 as installed and names DSH's own plugin installer as its update route. Before publication npm's latest is still 0.5.21, so the dialog marks 0.5.22 as a local version and offers no update. No restart notice appeared after **Enable now**, since the version that runs is the one installed.

Both hosts passed every step without a host warning or console error, and each host process was started once. [validation.json](validation.json) has the package digest and each host's results. Profiles and memories are synthetic.

![Status on DSH 0.2.0-rc.2](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.22/status.png)

![The CLI-written Native memory found by Direct recall](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.22/recall.png)

## The fixes in this release

Each fix has its own before-and-after record:
- [Updates through DSH's installer](../version-update-through-dsh/README.md): Check versions updates dsh-mnemon in the desktop app's Profile and reopens with the result after DSH swaps in the new page, and the restart notice names the installed and running versions.
- [A conversation tab without a loaded Agent](../conversation-tab-workspace/README.md): Status, Project Documents, new Documents and Runtime entries work in Conversation tab and Sidebar mode on both hosts.
- [Subagents and chat templates that require a user query](../issue-327-subagent-user-turn/README.md): idle review behind an Ollama 0.33 emulation recovers from the refusal, and healthy routes send the same requests as before.

## Validation and release boundary

`pnpm run release:check` confirms the Starter 0.5.22 on the `latest` tag with an unchanged composition; publication computes the changed packages from the previous release. No plugin uses a new SDK export from the Starter, so no peer floor moves. The version bump leaves the lockfile unchanged, and pnpm 10.13.1, the CI version, accepts it with `--frozen-lockfile`.

The release pull request and the publication workflow run the complete workspace and packed-plugin verification. Publication then:
- freezes the merged main revision;
- publishes the changed package and reads it back from npm;
- installs the complete 18-package combination and checks a real Registry upgrade;
- creates the GitHub release.

These screenshots show the versioned local package; they do not by themselves certify npm publication.
