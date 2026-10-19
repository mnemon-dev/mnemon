# Data directory: Default or Custom

[简体中文](./README.zh-CN.md)

Tested implementation: `edbffc58`, stacked on the Strategy names ([record](../strategy-names-20260927/README.md)). macOS 15.6, Node 25.1.0, published DSH 0.1.7-rc.2, headless Chrome at 1280 × 860 (and 420 × 900). Each run walks an isolated WebUI fixture from the Starter's defaults with its test model; the path shown is that fixture's disposable data directory. No personal memory or credentials were used.

## One directory on the row

The row showed where memory lives and, under it, an empty field that meant the default. Data directory is now a choice between Default and Custom, and shows only the directory memory uses. The default path sits under the title, with its end kept in view.

| Light | Dark |
|---|---|
| ![Default directory](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/data-directory-20260927/directory-default.png) | ![Default directory, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/data-directory-20260927/dark-directory-default.png) |

## Custom takes a path

Choosing Custom opens an empty field with the cursor in it. Apply waits for a path without calling the empty field a mistake; a path typed wrong says why.

| Custom, empty | Typed wrong |
|---|---|
| ![Custom with an empty field](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/data-directory-20260927/directory-custom.png) | ![A relative path](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/data-directory-20260927/directory-invalid.png) |

| A path ready to apply | Typed wrong, dark |
|---|---|
| ![An absolute path](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/data-directory-20260927/directory-typed.png) | ![A relative path, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/data-directory-20260927/dark-directory-invalid.png) |

## Narrow

| Default | Custom |
|---|---|
| ![Default, narrow](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/data-directory-20260927/narrow-directory-default.png) | ![Custom, narrow](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/data-directory-20260927/narrow-directory-custom.png) |

## Verification

- Unit tests cover the default path under the title, this workspace's directory under a central root, a custom path shown only in its field, Custom focusing an empty field with Apply disabled, returning to Default by its choice (unsetting a global directory, emptying a central root), relative, Windows drive and UNC paths, legacy Pack migration and read-only settings; a Host test covers the default directory the pack target reports.
- Full `pnpm run verify`, `pnpm run verify:plugins`, `pnpm run verify:docs` and `pnpm run release:intent` passed.
- WebUI: 4 light, 2 dark and 2 narrow states captured by a scripted walk with no console errors; the same flow was driven by hand in the in-app browser.

## Limits

Before a scope change is applied, the row names the destination only for the default directory; for Workspace and Centralized the Apply line says what applying does. The ZIP section and the Status page keep their own wording of the current root.
