# Cold start and upgrades with the component group

[中文](README.zh-CN.md)

Verified on 2026-09-29 (Asia/Shanghai) against `main` at `94d1e84f` (#313 and #314), with isolated profiles and synthetic data. DSH was not modified.

The candidate is 0.5.20 as `pnpm release:version` builds it; it is not published. A local registry serves npm's real metadata with every dsh-mnemon package's publish times moved two days back, plus 0.5.20. That is npm as it will read a day after the release, when 0.5.19 and its components are older than pnpm's one-day minimum release age. A pnpm wrapper sends the npm and npmmirror registries DSH asks to that registry. Every web and command-line run starts from a fresh `HOME`, DSH home and pnpm store.

## New users

| Way | DSH 0.2.0-rc.1 | DSH 0.1.7-rc.2 |
| --- | --- | --- |
| Web: **Plugins → Add plugin → dsh-mnemon → Install → Enable now** | Installed in 21.8 s, enabled in 0.8 s, one host process | 13.2 s and 1.1 s |
| Command line: `dsh plugin --profile web add dsh-mnemon`, then `dsh web` | Installed in 9 s, ready in 2 s | 8 s and 1.3 s |
| Headless: `scripts/verify-headless-profile.mjs --package dsh-mnemon@0.5.20` | Passed | Passed |
| Desktop layout: installed into its own generation, the profile links only the root, switched on in the Plugins list | Memory System without a restart | Same |

Every run ends with Status reading `dsh-mnemon 0.5.20`, nominal, and 10 components with no Starter row, and none reports a host warning or console error. The web runs also write a Runtime entry and read it back after a reload, create and activate a Native memory space, and find a fact written by the Mnemon CLI with **Direct recall**.

**The release day.** With 0.5.20 published an hour earlier, a plain install picks 0.5.19 on both hosts, and it works. That holds only once 0.5.19 itself is a day old, at 17:31 UTC on 09-29. Until then npm offers 0.5.17 to pnpm 11, because 0.5.18 and 0.5.19 were both released on 09-28. Checked against the real npm at 20:12 UTC on 09-28: `dsh-mnemon` and `dsh-mnemon@latest` install 0.5.17, which DSH 0.2 refuses, and only `dsh-mnemon@0.5.19` installs at once. The installation guide now says a plain install can fall back more than one release, and that DSH 0.1.7 accepts the older release silently.

## Existing users

Each profile first ran the older release with a Runtime entry written in the UI, the General strategy on and Project Documents off. For 0.5.18 and 0.5.19 the profile also had the Starter row, either on or turned off.

| DSH | From | Command, then a restart | Result |
| --- | --- | --- | --- |
| 0.1.7-rc.2 | 0.5.16 | `dsh plugin --profile web update dsh-mnemon` | 0.5.20; choices kept |
| 0.1.7-rc.2 | 0.5.17 | same | Runtime entry and choices kept |
| 0.1.7-rc.2 | 0.5.18, Starter row on | same | Kept; the Starter row is gone |
| 0.1.7-rc.2 | 0.5.19, Starter row on | same | Kept; the Starter row is gone |
| 0.1.7-rc.2 | 0.5.19 pinned, Starter row off | `update --latest` | Recovered from the stuck state |
| 0.2.0-rc.1 | 0.5.19, Starter row on | `update` | Kept; the Starter row is gone |
| 0.2.0-rc.1 | 0.5.19 pinned, Starter row off | `update --latest` | Recovered from the stuck state |

No run reports a host warning. The Headless upgrade check passes from 0.5.16, 0.5.17, 0.5.18 and 0.5.19 on DSH 0.1.7-rc.2, and from 0.5.19 on 0.2.0-rc.1, with the Runtime memory file unchanged byte for byte. The 0.5.16 web profile names its tab 运行时, so its Runtime continuity comes from this check.

**Desktop layout.** A 0.5.19 generation with the Starter row turned off reproduces the stuck state on both hosts: no Memory System, all 11 components off, and `mnemon-bundle` waiting for `mnemonStarterReady`. Moving the link to a 0.5.20 generation and restarting brings everything back.

| Stuck on 0.5.19 | After the 0.5.20 generation and a restart |
| --- | --- |
| ![Stuck](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/starter-group-upgrade/desktop-stuck-0519.png) | ![Updated](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/starter-group-upgrade/desktop-updated-0520.png) |

**Updating without a restart.** When the link moves while DSH runs and a component is then switched, the Loader keeps the running group's old module. That group waits for the removed readiness service, so the Memory System disappears until DSH restarts:

![mnemon-bundle (dsh-mnemon/bundle) waiting for mnemonStarterReady after an update without a restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/starter-group-upgrade/in-place-no-restart.png)

The installation guide, the operations table and the compatibility reference now name this message and send it to a restart rather than to the Starter switch, which 0.5.20 no longer has.

[validation.json](validation.json) holds the candidate digest, the registry setup and every result.
