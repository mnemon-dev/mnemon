# Mnemon memory panel for Codex

A local MCP App for inspecting Mnemon stores and associating projects.
Declares a native sidebar (`global`) entrypoint and a conversation-panel
(`thread`) entrypoint. New memories appear automatically; filtering, selection
and scroll positions survive data updates. No model calls are needed for sync.
Optional explicit writes and a bundled development skill complete the memory
loop: recall before work, save verified project lessons after work, and keep
transferable global experience in a separate library.

## Requirements

- Node.js 22 or newer on the desktop app's PATH.
- Mnemon CLI, currently verified with **0.2.10**.
- Python 3.9 or newer for SQLite online backup (standard library only).
- An MCP Apps host supporting OpenAI global/thread entrypoints. Installed Codex
  desktop **26.1002.7124.0** recognizes both descriptors; native visible placement
  depends on the host. The global page is confirmed visible; an old conversation
  reported an unknown MCP server before loading its thread panel. Native thread
  rendering was verified after the 0.2.3 repair. Entry placement remains
  host-controlled.

## Install locally

From this directory:

```sh
npm ci
npm run install:codex -- my-project-store
```

The installer preserves an existing personal marketplace, copies this package
to `~/.codex/plugins/local/mnemon-memory-panel`, and installs it with
`codex plugin add`. It writes machine-specific settings only into that local
copy. `CODEX_CLI_PATH` can select the desktop app's CLI executable.

Reload plugins or restart the desktop app, find **Mnemon** in the local plugin
source, and open its sidebar entry or conversation-panel entry. A host determines
the exact entry placement; this package cannot force arbitrary desktop chrome.
Selecting a store in this panel never changes Mnemon's global active store.

### Keeping a left sidebar shortcut

The OpenAI extension contract defines `global` as a fullscreen app and `thread`
as a content tab. The installed Codex client places plugin thread entries inside
**More tools**. This plugin cannot promote itself below Terminal, remove that
host menu, or redirect a global navigation click into an existing chat's right
panel. These requested placements require host support; the package does not
patch Codex application files.

The installed desktop client includes a **Pin to sidebar** action in the
right-hand content tab's context menu. Right-click the Mnemon **tab title**, not
the plugin settings page, and select that action if available. It creates a
reference to that exact tab in the sidebar's Pinned section. The shortcut's
availability depends on the host's feature settings and the owning conversation;
it is not a separate plugin preference. A removed or unavailable source tab may
leave an unavailable shortcut. This route depends on the host and must not be
described as an unconditional global app entry.

### An old conversation says “Could not display Mnemon”

The observed desktop log reports `unknown MCP server 'mnemon-memory-panel'`
for both resource and tool requests scoped to the old conversation. Global
navigation can succeed while that conversation retains an earlier MCP server
configuration. Reload plugin configuration, or restart Codex and reopen that
conversation, then retry. No thread/model messages need to be sent for this.
An SDK/resource roundtrip alone does not confirm that an old conversation has
refreshed its server configuration.

A later retry also exposed an active older server reading UI files removed
by a package upgrade (`ENOENT` in the versioned cache). Version 0.2.3 loads
the complete MCP HTML resource at startup, and the local installer retains
previous version runtime files through upgrades. Existing affected processes
need their files restored or a restart. This repair does not register a missing
server in an old conversation; that remains a separate host configuration issue.
On Windows, reinstalling the same version can fail if the host holds its cache
directory open. Close/restart the host before that operation rather than deleting
the active cache. Retained old versions can be cleaned after old sessions end.

## Projects and global experience

- **A project:** its own store, selected by an explicit directory association.
  Unassociated discovered projects get a stable path-based store proposal,
  never a guess based on a same-named folder. New stores are created on the
  first explicit write. Existing stores can be associated without migrating
  records or changing Mnemon's active store.
- **Global experience:** the separate reserved `codex-global` store. Keep
  genuinely transferable lessons here. Projects cannot bind to this name,
  including case variants on Windows.
- **All projects:** a read-only aggregate of existing stores, with provenance
  on each record. It is not a writable store or a copy of all projects.
- **Choose project:** inspect discovered projects or paste a complete directory,
  then associate its store. Manual associations survive missing host discovery.

Discovery first uses available MCP roots and an optional read-only Codex saved
project catalog, isolated in `server/projects.mjs`. That host file is not a
guaranteed public API: unsupported/malformed future layouts fall back to saved
associations and manual directory entry. Explicit associations live under
`<Mnemon data root>/codex-panel/projects.json`, outside project source.
An explicitly associated ancestor also covers its nested checkouts unless a
more specific explicit association exists.

The `mnemon-project-memory` skill accompanies the MCP tools `mnemon_context`
and `mnemon_remember`. The former recalls the current project and global
library; the latter writes to an explicit scope after verification. The panel's
small **Supplement memory** dialog is optional, not the primary workflow.
Hosts decide skill selection, so this is guidance rather than a guarantee of
automatic extraction every turn. Existing hooks remain separate and unchanged;
their configured store must agree with the association when used together.

On Windows use PowerShell 7. If PATH aliases for Python or Mnemon are unusable,
configure the executable explicitly before installation:

```powershell
$env:MNEMON_UI_PYTHON = 'C:\path\to\python.exe'
$env:MNEMON_UI_COMMAND = 'C:\path\to\mnemon.exe'
npm run install:codex -- my-project-store
```

## Local preview

```sh
npm run preview
```

Open `http://127.0.0.1:43187`. This preview uses the same HTML and data service
as the MCP App, but does not itself create the native sidebar entry.
The HTTP listener accepts loopback requests only, checks Host/Origin, and is not
required by the installed stdio plugin. Explicit project association and memory
write endpoints require same-origin JSON POST requests; browsing stays read-only.

## Configuration

| Variable | Purpose |
| --- | --- |
| `MNEMON_UI_STORE` | Initial named store; defaults to `MNEMON_STORE`, then `default` |
| `MNEMON_UI_DATA_DIR` | Data root; defaults to `MNEMON_DATA_DIR`, then `~/.mnemon` |
| `MNEMON_UI_COMMAND` | Native executable or npm launcher `.js` path |
| `MNEMON_UI_PYTHON` | Python 3 executable; Windows `python`, other systems `python3` |
| `MNEMON_UI_PORT` | Preview port; defaults to 43187 |
| `MNEMON_UI_HOST_STATE` | Optional saved Codex project catalog path; discovery-only |

## Data safety and upgrade compatibility

The UI consumes a versioned normalized snapshot. Only `server/adapter.mjs`
knows Mnemon command/output shapes. Extra JSON fields are tolerated; optional
fields have defaults; missing required fields fail visibly and retain the last
successful display. The executable version is checked periodically. Unknown
versions are interface-validated, not claimed to be fully release-tested.

Mnemon 0.2.10's `--readonly` uses immutable SQLite access and must not observe a
database being actively changed. Therefore the adapter discovers the path via
the CLI, copies a consistent live snapshot using SQLite online backup, and
then runs public `--readonly status` and `--readonly recall --basic` commands
against that temporary copy. It never queries Mnemon tables directly. Access
counters, operation logs and source content are unchanged. The SQLite reader
may use SQLite-managed WAL/SHM bookkeeping while a writer is active.

Snapshots are removed after reads. A future official live-read interface or
different storage backend needs an adapter change, not a UI rewrite. No package
can guarantee compatibility with arbitrary future breaking engine changes.
Browsing never upgrades Mnemon or migrates its source database. Explicit writes
use the official writable CLI and its normal store initialization behavior.
Cross-process locks serialize this plugin's project associations and same-store
writes; external clients retain their own concurrency semantics.

## Automatic updates

The service watches the discovered store directory and polls file metadata every
2 seconds as a fallback. It debounces changes and publishes a content revision.
The UI keeps one long-poll request open through the MCP Apps bridge (at most
20 seconds), receiving changed data immediately when the adapter refreshes.
This is **change detection plus long polling**, not a claim that Mnemon exposes
a native event stream. Preview HTTP follows the identical contract.

Error states distinguish an empty store from a failed connection, preserve
previous data, and retry automatically. Results are bounded to 5000 active
memories, 8 cached stores, 15-second CLI timeouts and bounded output. Large
stores show a truncation notice; search applies to the loaded set. Aggregate
browsing covers at most 64 stores and shows at most 5000 memories.

## Local verification and acceptance

Run `npm test` in this directory for the portable MCP resource upgrade and
engine-output compatibility checks. They use synthetic records and temporary
files, without accessing or writing a Mnemon memory store.

Verified locally on Windows, Node 22, Python 3.13 and Mnemon 0.2.10:

- Real named-store count matches loaded memories; Chinese content renders.
- Empty store is displayed as empty, not a connection error.
- A committed WAL record is included in the online backup; source DB bytes unchanged.
- Separate CLI writes unblock a waiting revision request in approximately 1 second.
- Extra fields are accepted; missing required fields fail closed.
- MCP SDK roundtrip returns both global/thread descriptors, HTML resource,
  real memory data and unchanged heartbeats.
- Codex app-server lists the installed, enabled plugin and its tools with no
  Mnemon tool-discovery errors.
- A real memory addition updates the open preview without reload, preserving
  the active search and selected memory.
- A simulated initial MCP bridge failure recovers on the second initialization
  attempt and displays the real store.
- Reinstalling without a store argument preserves the previously selected store
  and the installer carries forward configured executable/data paths.
- Project/global records stay separate; uninitialized scopes are empty without
  creating stores; aggregate writes and project binding to global are rejected.
- Concurrent identical plugin writes are serialized and duplicates are skipped.
- An active MCP resource stays readable after its package UI directory is replaced.
- Version 0.2.3 installs successfully while retaining the older runtime UI files.

Native right-panel rendering was verified after the 0.2.3 repair. Automatic
updates were verified in the equivalent local preview; native rendering alone
does not establish live-update behavior in every host session. The global
left-rail entry still opens fullscreen, and the thread launcher remains under
More tools. These are host-controlled limitations, not configuration options
provided by this package. The Go product core is not modified.

## Contributing upstream

This optional integration is proposed for maintainer discussion in
[issue #155](https://github.com/mnemon-dev/mnemon/issues/155). Follow the root
`CONTRIBUTING.md` and run the required upstream checks before submission.
Do not include local settings, real memories, databases, private screenshots,
logs or `node_modules` in a contribution.
