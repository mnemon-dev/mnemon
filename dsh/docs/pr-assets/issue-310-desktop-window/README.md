# Desktop windows manage memory — issue #310

[中文](README.zh-CN.md)

Verified on 2026-09-29 (Asia/Shanghai) with isolated profiles and synthetic data. DSH was not modified.

## Problem and fix

DSH Desktop loads its window from the app's own `dsh-app://app/` address, and the DSH 0.1.7 app declares no transport for the page. dsh-mnemon 0.5.18 read that page as remote, sent every Mnemon call through the API Gateway, and the default `remoteAccess: read-only` turned Runtime memory, Memory Spaces and the plugin settings read only, while agent tools could still write ([#310](https://github.com/omdsh-dev/dsh-mnemon/issues/310)).

From `b5f7914b`, a page the application serves itself uses Mnemon's local channels; it counts as remote only when DSH declares a transport for it that does not own the Host. A page opened from another machine keeps the Gateway and its read-only default, and now says which grant would let it manage memory.

| Published 0.5.18 in a DSH 0.1.7 Desktop window | The fix in the same window |
| --- | --- |
| ![Runtime memory says the deployment is read only and has no Add memory](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-310-desktop-window/before-runtime.png) | ![Add memory is back and a new entry was written](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-310-desktop-window/after-runtime.png) |
| ![The dsh-mnemon page under Plugins says its settings are read only](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-310-desktop-window/before-plugin.png) | ![The dsh-mnemon page under Plugins is editable](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-310-desktop-window/after-plugin.png) |

## Environment

- Shell: [`scripts/fixtures/electron-desktop-window.mjs`](../../../scripts/fixtures/electron-desktop-window.mjs) in Electron 44.3.0 registers `dsh-app` as a standard, secure, CORS, fetch and stream scheme, as the official shell does, and proxies the window's requests to `dsh web` listening only on 127.0.0.1. `DSH_TRANSPORT=owns-host` also declares the DSH 0.2 Desktop transport, `{ ownsHost: true, streamBaseUrl }`.
- Hosts: published DSH 0.1.7-rc.2 and 0.2.0-rc.1, each installed with npm into an isolated prefix, with a fresh DSH home and profile for every run.
- Packages: the baseline is dsh-mnemon 0.5.18 as published; the candidate is the 0.5.19 release that `pnpm release:version` builds from `b5f7914b`, installed from its packed tarballs.
- Driver: the DevTools protocol opens Memory System, adds one runtime memory, reads Memory Spaces and the plugin page, and records the URL of every Mnemon request. [validation.json](validation.json) has each run's results.

## Results

| Window | Package | Runtime memory | Memory Spaces | Plugin settings | Mnemon requests |
| --- | --- | --- | --- | --- | --- |
| DSH 0.1.7-rc.2 Desktop | 0.5.18 | Read only, no Add memory | Save and Create absent | Read only | 13 through the API Gateway |
| DSH 0.1.7-rc.2 Desktop | 0.5.19 | Add memory writes | Save and Create enabled | Editable | Local channels; the write is `POST dsh-app://app/dsh-mnemon-write/…` |
| DSH 0.2.0-rc.1 Desktop, `ownsHost: true` | 0.5.19 | Add memory writes ([screen](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-310-desktop-window/after-dsh-020-runtime.png)) | Save and Create enabled | Editable | Local channels |
| Remote page through `--trusted-host`, DSH 0.2.0-rc.1 | 0.5.19 | Read only, with the reason ([screen](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-310-desktop-window/remote-runtime.png)) | Save disabled | Read only, with the reason ([screen](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-310-desktop-window/remote-plugin.png)) | 14 through the API Gateway |

No run logged a console error. DSH shows a 0.1.7 window as reconnecting because its WebSocket stream cannot open through the scheme in this shell; Mnemon's requests are plain HTTP and unaffected, and the screens are cropped to the Memory System.

## Limits

This is a shell that behaves like DSH Desktop for the page origin, the scheme and the request path; it is not the Desktop installer. The Host admits the local channels and `/api` with the same Host/Origin checks and browser session, so the desktop result does not depend on a Desktop-only grant.
