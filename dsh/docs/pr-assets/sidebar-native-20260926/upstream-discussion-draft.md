# Native group containers appear as unusable bundle component switches

Local draft only; no upstream post or pull request has been submitted.

DSH `0.1.7-rc.2` shows a named native `cordis:group` as a disabled component on the Plugins bundle detail page, even though the group's plugins are active. Clicking that switch returns `unknown-plugin` (Chinese UI: `组件启用失败：找不到该插件`). The affected Mnemon Starter shows nine components: eight real plugins and one internal `mnemon-bundle` group.

This reproduces with the published npm runtime on macOS and Node `24.20.0`. Current upstream master is the rc.2 release commit [`477b4f420553e8a52c2fbccc464d7561b239c443`](https://github.com/deepseek-ai/deepseek-harness/commit/477b4f420553e8a52c2fbccc464d7561b239c443), which has the same behavior.

## Reproduction

Create a local bundle with these files, install it into a disposable Web profile with `dsh plugin --profile web add /absolute/path/to/grouped-demo`, and inspect its Plugins detail page.

`package.json`:

```json
{
  "name": "grouped-demo",
  "version": "1.0.0",
  "type": "module",
  "dsh": { "bundle": { "patch": "./cordis.patch.yml" } }
}
```

`cordis.patch.yml`:

```yaml
- insert:
    - id: demo-group
      name: cordis:group
      group: true
      config:
        - id: demo-child
          name: ./plugin.mjs
```

`plugin.mjs`:

```js
export function apply(ctx) {
  ctx.provide('groupedDemo', true)
}
```

Expected: the bundle lists the manageable `demo-child` plugin. The group still owns its children and applies inherited configuration/disable behavior.

Actual: the bundle also lists `demo-group`. That row has no corresponding manageable plugin and its switch fails. Its apparent off state does not establish that the group's children are inactive.

## Cause and proposed fix

[`readPluginInventory`](https://github.com/deepseek-ai/deepseek-harness/blob/477b4f420553e8a52c2fbccc464d7561b239c443/packages/host/plugin-inventory/src/index.ts#L86) deliberately excludes entries with `options.group`. [`declaredRows`](https://github.com/deepseek-ai/deepseek-harness/blob/477b4f420553e8a52c2fbccc464d7561b239c443/packages/boot/plugin-manager/src/index.ts#L645) includes those same native containers in `BundleInfo.rows`, so the page receives a component that `setPluginEnabled` cannot find.

The proposed fix excludes group containers from the displayed bundle rows while continuing to traverse their children. It retains every inserted row ID, including groups, when classifying external overrides; a bundle patch targeting its own group must not become an external override.

```diff
 const rows: BundleRowInfo[] = []
+const declared = new Set<string>()
 const packages = this.ctx.get('pluginPackages')
 for (const row of flatten(composeEntries([patches.filter(item => item.insert !== undefined)]))) {
   if (typeof row.id !== 'string' || typeof row.name !== 'string') continue
+  declared.add(row.id)
+  if (row.group) continue
   // Existing live entry and display metadata projection.
 }
-const declared = new Set(rows.map(row => row.rowId))
```

The fix changes the inventory projection only. Stable IDs, nested patch addressing, disabled expressions, child ownership, bundle selection and component persistence remain with the existing Loader and manager. Flattening the Mnemon composition would leave enabled children waiting for the disabled core. Removing the group ID also failed a live-reload lifecycle check, so neither workaround is included.

## Local verification

- A new test in `packages/boot/plugin-manager/tests/manager.spec.ts` failed before the fix because both a group and its nested group appeared in component rows. After the fix, the test verifies selected and deselected bundle rows, a disabled child, real child off/on, bundle teardown, and correct external override classification.
- `pnpm exec vitest run packages/boot/plugin-manager/tests/manager.spec.ts packages/host/plugin-inventory/tests/inventory.spec.ts --maxWorkers=1`: all 88 tests passed.
- Focused V8 coverage of `packages/boot/plugin-manager/src/index.ts`: all 81 owning tests passed, with 100% statements, branches, functions and lines.
- The manager's TypeScript dependency closure compiled, its own runtime bundle built through the repository's normal tsdown configuration, and focused lint passed.
- `pnpm run doc-sync`: all 42 documentation checks passed, including the updated bilingual README pair.
- The existing real WebUI fixture now nests its three plugins under a stable native group; the expected component output is unchanged. A separately isolated official rc.2 installation with only the locally built manager candidate was also exercised through the real WebUI with all 17 packed Mnemon artifacts: the list showed eight real components, Documents could be disabled and re-enabled with its page removed and restored, and toggling the whole bundle off/on changed all eight components to off and back to running. The strict bundle-row diagnostic and the complete lifecycle/restart fixture passed against that candidate.
- Mnemon's separate regression passes against published DSH `0.1.5-rc.1` and `0.1.7-rc.2`: Source/Strategy activation sentinel/core/bundle manager toggles, restart persistence, independent component choices, literal and expression-based legacy core flags, private Provider teardown and duplicate-instance checks. It does not alter the installed DSH packages.

The local candidate package retains the version `0.1.7-rc.2`; it is not a published upstream release. Its SHA-256 is `bf06f067cf056d71e20a422d3d40957f88d26bbc02a3714cc738c94361ef8513`.

Implementation and review used OpenAI Codex with GPT-6 Astra.
