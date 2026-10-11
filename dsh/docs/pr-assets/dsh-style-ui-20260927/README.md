# DSH-style memory interface

[简体中文](./README.zh-CN.md)

Tested implementation: `0213857a7f7d`, stacked on the optional Mnemon CLI change (`adeb9f701374`). macOS 15.6, Node 25.1.0, pnpm 11.19.0, published DSH 0.1.7-rc.2 and Mnemon CLI 0.2.7, headless Chrome 153 at 1280 × 860 unless noted. The WebUI fixture holds the Provider Lab data from the [optional Mnemon CLI record](../optional-mnemon-cli-20260927/README.md): Mnemon Native plus Docker services for OpenViking, Honcho, Mem0, Hindsight, RetainDB and Supermemory, and a local Holographic store. No personal memory or credentials were used.

"Before" captures come from the base revision in the same fixture; the turn memory bar before this change is the one recorded for #293, which later changes left untouched.

## Settings → Memory System

| Before | After |
|---|---|
| ![Settings before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-before.png) | ![Settings after](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-after.png) |

Settings is now a column of DSH preference rows in six groups. Placement, storage scope and user profile scope are DSH Menu selectors instead of choice cards, and every switch is the DSH Switch.

| Storage | Memory providers |
|---|---|
| ![Storage group](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-storage.png) | ![Provider list](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-providers.png) |

Storage holds the scope, the single Data directory field (empty means the default location), the user profile scope and ZIP backup and migration; none of them sit in the Mnemon Native panel any more. Mnemon Native is the first Provider card. Expanded, it holds only its embedding settings, in the DSH editor block:

![Mnemon Native expanded](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-native.png)

![Background tasks](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-background.png)

The task Agent model and idle review share Background tasks; the review options appear while review is on, with its limits folded.

## Status

| Before | After |
|---|---|
| ![Status before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/status-before.png) | ![Status after](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/status-after.png) |

Mnemon Native is the first row of the one Memory providers list; the Provider count includes it. The dark theme uses the same tokens:

![Status in the dark theme](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/status-dark.png)

## Memory Spaces

| Before | After |
|---|---|
| ![Spaces before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/spaces-before.png) | ![Spaces after](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/spaces-after.png) |
| ![Entities before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/entities-before.png) | ![Entities after](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/entities-after.png) |

Cards drop the provider stripes and gradients, use DSH Tags, state dots and switches, and show delete and disconnect as DSH card delete actions. Pages under the tabs title their sections at the group heading scale. Entity capability cards mark unsupported Providers as idle instead of failed.

## Plugins page and conversation

| Before | After |
|---|---|
| ![Plugins page before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/plugins-before.png) | ![Plugins page after](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/plugins-after.png) |
| ![Turn memory before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/turn-before.png) | ![Turn memory after](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/turn-after.png) |

The main Strategy is a selector row and the enhancements are switch rows, as on DSH's own component list below them. The turn memory bar uses the DSH data icon and chevron with neutral tool Tags. The save dialog edits its candidate in a DSH settings field:

![Save to memory dialog](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/save-dialog.png)

## Validation

- `pnpm run verify`: documentation 2,144 local links and 81 anchors; deterministic build of 42 files; build, typecheck and tests for the workspace packages (Memory Spaces 174 passed with 2 conditional skips, Runtime 61, Documents 39); root suite 1,419 passed with 6 conditional skips; real Headless activation (37 tools), legacy settings import with an idempotent restart and the root disable gate; 52 packed files, publint strict and attw.
- `pnpm run verify:plugins --skip-build`: 17 independent plugin repositories and 18 packed artifacts, including real DSH activation of the packed Starter and of the three optional Strategies.
- `pnpm run release:intent`: change intents cover every changed package.
- WebUI: 16 states each in the light and dark themes at 1280 × 860 and in the light theme at 700 × 900, every Settings group in both themes, a driven conversation turn with the save dialog, and a memory-tool turn; no console errors in any run. Builtin placement was switched on in Settings, opened from a conversation tab, and switched back.

## Limits

Screenshots come from one seeded fixture and show layout and styling, not Provider behavior. The Native Provider still labels its spaces "mnemon", from its descriptor. The documentation media for v0.5.4 predate this change and are refreshed with the next release media. Builtin placement keeps the DSH conversation tabs above the Memory System tabs, as before.
