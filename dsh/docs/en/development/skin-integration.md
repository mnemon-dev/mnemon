# Skin development and Mnemon integration

**English** | [简体中文](../../zh-CN/development/skin-integration.md) | [Development and verification](./README.md)

This guide is for authors who want the Mnemon workbench to match their host skin. Mnemon provides a stable page selector and background properties; the skin chooses colors, transparency and readability. Installing an existing skin does not automatically enable a glass effect for Mnemon. The skin must supply an explicit override.

## Public contract and scope

The Sidebar and the conversation tab share this root selector:

```css
[data-dsh-plugin="dsh-mnemon"][data-dsh-part="mnemon-view"]
```

The supported background properties are `--mn-bg`, `--mn-backdrop` and `--mn-surface`. The [UI guide's skin contract](../guides/ui-guide.md#theme-skin-overrides) defines their value types, default composition, cascade priority and inheritance scope. Without overrides, the default background remains and the official theme stays opaque.

This contract was introduced by [PR #284](https://github.com/omdsh-dev/dsh-mnemon/pull/284); test with a build containing that change. Older versions may lack the root marker, in which case the rule does not match. CSS-module hashes, unlisted internal properties and DOM nesting are not compatibility interfaces.

Background integration belongs in the skin's Client styles. It needs no new Mnemon setting or changes to Host, Source, Strategy, Provider or storage formats. The root rule does not cover other plugins or dialogs portaled to `body`; dialogs retain their own surface styles.

The native Sidebar entry is rendered by DSH through `sidebar.panellist`, just like Plugins. The skin's native panel-row rules therefore supply its color, typography, spacing, selection and collapsed presentation. Mnemon supplies only its label and size-responsive icon; the workbench background contract above applies to the opened page.

A replacement layout without the native panel seat gets Mnemon's own entry under New Session instead: `[data-dsh-plugin="dsh-mnemon"][data-dsh-part="sidebar-entry"]`. It copies DSH's panel-row values: inherited 14px type, primary label color, 36px rows with `7px 8px` padding and `0 2px` margins, `--dsw-radius-md` corners, a 16px icon (18px on the collapsed rail), and the hover background for both hover and the open workspace. The open state also sets `data-active` and `aria-current="page"`. A skin that restyles native panel rows can target this selector to match.

The native main seat reserves DSH's `--dsh-frame-top-clearance` once at its outer boundary, keeping desktop window controls accessible. Its page layer sits above decorative body layers and below DSH window chrome; skins should not add another window-title offset to the inner workbench.

## Add an override to your skin

1. Put the rule in a CSS file that the skin actually loads, installs, switches and removes. Do not edit Mnemon source or generated `lib/` files.
2. Scope it to the skin's own activation marker, followed by the complete Mnemon root selector. The rule then stops matching when the skin changes.
3. Assign the properties on the Mnemon root element. Assigning `--mn-*` only on `html` or another ancestor is shadowed by the root's defaults.

For example, the v2 skin format in the [dsh-web skin center](https://github.com/zhu1090093659/dsh-skins) uses `html[data-dsh-skin="<id>"]` as its activation marker. Put the integration rule in the `patches.css` file referenced by `skin.json` through `contributes.patches`. Append to an existing file without replacing its contents; when adding one, add only the relevant field to the existing manifest.

Replace the example ID `my-glass-skin` below with the `id` in your own `skin.json`. This example assumes that the skin already sets `--dsw-alias-bg-base` to a suitable translucent color for both dark and light modes:

```css
html[data-dsh-skin="my-glass-skin"]
[data-dsh-plugin="dsh-mnemon"][data-dsh-part="mnemon-view"] {
  --mn-bg: var(--dsw-alias-bg-base);
  --mn-backdrop: var(--mn-bg);
  --mn-surface: var(--mn-bg);
}
```

The rule replaces the default backing composition with the skin's base color in workbench regions that consume `--mn-surface`. A fully transparent base produces a fully transparent result, so skin authors must choose suitable opacity. Wallpaper and blur remain the skin's responsibility; these three properties do not create them. The dsh-web file format and activation marker belong to the skin center. Other skin systems should use their own loading mechanism and activation scope with the same Mnemon root selector.

Keep the rule in an unlayered stylesheet and retain both Mnemon attributes. Those two attributes alone have higher specificity than the defaults, so the override needs neither a particular stylesheet order nor `!important`. A normal declaration inside `@layer` ranks below Mnemon's unlayered defaults.

## Migrate from generated class names

For an existing `[class*="_shell"]` rule, keep the skin's background calculation and replace the target with the public root selector scoped to the active skin. Remove the broad old match so it cannot affect similarly named internal layouts in other plugins. Both placements use the same rule; the Sidebar and the conversation tab need no separate skin implementations.

For example, `--pg-panel-rgb`, `--pg-bg-glass` and `--pg-glass-floor` in Issue #273 belong to that skin. Mnemon does not provide them. Keep their definitions in the skin when migrating; do not add them to Mnemon configuration.

## Verify in the real WebUI

Use an isolated environment from the [development and verification guide](./README.md). Install the skin and Mnemon artifacts intended for release, then perform these checks with synthetic memory:

| Check | Expected result |
|---|---|
| Default theme and disabled skin | The default surface remains, without leftover skin overrides |
| Sidebar and conversation tab | The same root selector matches the current workbench; navigation works in both placements |
| Dark, light and narrow windows | Text, buttons and inputs remain readable; backgrounds and responsive behavior match the skin's design |
| Runtime add, edit and reload | Operations succeed and the record survives reload |
| Open and cancel dialogs | Dialogs stay readable; the workbench remains usable after closing them |
| Plugin composition and return to conversation | Mnemon rules do not alter peer plugin pages or conversation inputs |
| Skin switching and reload | Activation scope follows the skin and matches the persisted choice |

Capture default and customized screenshots with the same page, data, color mode and viewport. Record Mnemon, DSH, skin-center and skin versions. Clearly distinguish an original skin preview from a preview with added integration CSS.

If the override does not apply, check whether the skin is actually active, the root marker exists and the stylesheet loaded, then inspect the computed `--mn-surface` in developer tools. For crossed-out rules, check the selector and `@layer`. If the computed value changed but wallpaper is absent, check the skin's own background, scrim and responsive rules. Record skin installation or settings-save failures separately; they do not establish that the Mnemon hook failed.

When maintaining Mnemon itself, also run the existing targeted regression and documentation checks:

```sh
pnpm exec vitest run tests/client.spec.tsx -t 'accepts scoped skin variables'
pnpm verify:docs
```

The targeted regression covers both placements and style isolation from peer plugins. Actual painting, stylesheet order and readability still need real browser verification. Existing reproduction steps and before/after evidence are in the [Issue #273 acceptance record](../../pr-assets/issue-273-skin-hooks/README.md).
