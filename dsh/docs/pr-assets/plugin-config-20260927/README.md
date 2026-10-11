# Configuration on the dsh-mnemon page under Plugins

[简体中文](./README.zh-CN.md)

Tested implementation: `7e23e37f`, stacked on the DSH-style interface (`74b214eb`). macOS 15.6, Node 25.1.0, pnpm 11.19.0, published DSH 0.1.7-rc.2 and Mnemon CLI 0.2.7, headless Chrome 153 at 1280 × 860 unless noted. The WebUI fixture holds the Provider Lab data from the [DSH-style interface record](../dsh-style-ui-20260927/README.md): Mnemon Native plus Docker services for OpenViking, Honcho, Mem0, Hindsight, RetainDB and Supermemory, and a local Holographic store. No personal memory or credentials were used.

"Before" is the base revision as recorded for the DSH-style interface.

## Configuration moves under Plugins

| Before: Settings → Memory System | After: Plugins → dsh-mnemon |
|---|---|
| ![Settings before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-after.png) | ![The dsh-mnemon page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-config-20260927/plugin-page.png) |

DSH 0.1.7 edits a plugin's own configuration on its page under Plugins and keeps Settings for DSH's sections and the read-only plugin inventory. The Memory System section is gone from Settings:

![Settings without a Memory System section](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-config-20260927/settings-dialog.png)

The dsh-mnemon page (titled 可组合记忆 in Chinese) shows the plugin's title and description, then the configuration in six groups (Strategy, Memory layers, Memory providers, Storage, Background tasks, Interface), then the components the Starter includes. Before this change the page carried only the Strategy rows and a pointer back to Settings:

| Plugins page before | The whole page after |
|---|---|
| ![Plugins page before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/plugins-after.png) | ![The whole dsh-mnemon page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-config-20260927/plugin-page-full.png) |

## Unsaved changes

Groups with Save stage their edits; a bar floats 16 px above the bottom of the page while the configuration is in view and rests after its last group. Leaving the page drops the staged edits.

| Floating over the page | At rest before the components |
|---|---|
| ![Floating save bar](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-config-20260927/plugin-page-floating.png) | ![Components after the configuration](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-config-20260927/plugin-page-components.png) |

## Between the workspace and its configuration

The page head offers **Open Memory System** (`plugins.detail.actions`), and the Memory System header offers **Configure**, the gear next to refresh, through `ctx.pluginNavigation.openBundle('dsh-mnemon')`. A switched-off memory layer page links to the configuration too.

![Memory System header with Configure](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-config-20260927/workspace-configure.png)

## Themes and narrow layout

| Dark theme | Dark theme, unsaved | 700 × 900 |
|---|---|---|
| ![Dark theme](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-config-20260927/dark-plugin-page.png) | ![Dark theme with the save bar](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-config-20260927/dark-plugin-page-unsaved.png) | ![Narrow layout](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-config-20260927/narrow-plugin-page.png) |

## Remote pages

Chrome mapped `memory.test` to 127.0.0.1 and the fixture ran with `pnpm e2e:serve --trusted-host=memory.test:<port>`, so DSH and Mnemon treated the page as remote. The DSH Plugins page, the configuration and both links work there.

- Default (`remoteAccess: read-only`): the read-only notice leads the page, every control including the Strategy rows is disabled, and a click changes nothing.
- With `--remote-management` (`remoteAccess: trusted-host`): a Save of the Save to memory switch persisted through the API Gateway across a page reload and was then restored.

![Remote page without the management grant](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-config-20260927/remote-readonly-plugin-page.png)

## Live refresh

Two browser tabs on the same loopback WebUI both showed the dsh-mnemon page. Saving the Save to memory switch in the second tab updated the first tab without a reload, through DSH's `configForms` revision of the `mnemon` entry; restoring it synchronized back.

## Validation

- `pnpm run verify`: documentation 2,216 local links and 83 anchors; deterministic build of 42 files; build, typecheck and tests for the workspace packages (Memory Spaces 174 passed with 2 conditional skips, Runtime 61, Documents 39); root suite 1,424 passed with 6 conditional skips; real Headless activation (37 tools), legacy settings import with an idempotent restart and the root disable gate; 52 packed files, publint strict and attw.
- `pnpm run verify:plugins --skip-build`: 17 independent plugin repositories and 18 packed artifacts, including real DSH activation of the packed Starter and of the three optional Strategies.
- `pnpm run release:intent`: change intents cover every changed package.
- WebUI: 9 light, 8 dark and 8 narrow states on the loopback fixture and 8 states on each remote fixture, with no console errors in any run.

## Limits

Screenshots come from one seeded fixture and show layout and styling, not Provider behavior. The isolated fixture profile leaves DSH's permission presets unavailable, so General in Settings shows DSH's own unavailable notice. Builtin placement opens the workspace from the plugin page only while a conversation is current. Components of the Starter still show their package names on the plugin page; localized row titles and icons from each package's display metadata are a follow-up.
