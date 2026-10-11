# Better Sidebar compatibility verification (#153)

This directory records the real WebUI acceptance check for the optional
`dsh-better-sidebar` integration. Both profiles used test-owned `DSH_HOME`,
workspace, and Mnemon data directories under the issue worktree. They did not
read from or write to the personal Mnemon data root.

## Environments

| Case | Harness | Better Sidebar | Client bundle order |
| --- | --- | --- | --- |
| Reported-version acceptance | `0.1.1-rc.2` | `0.18.1-alpha.0` (`b2722ece`) | Better Sidebar, then Mnemon |
| Reverse-order acceptance | `0.1.2-alpha.3` | `0.18.1-alpha.0` (`b2722ece`) | Mnemon, then Better Sidebar |

The reported-version profile also used `@linxin666/dsh-web-all@0.3.6`. Mnemon
was linked from this worktree after `pnpm run build`. Writes and lifecycle
automation were disabled; storage used a profile-owned custom directory.

## Results

- The normal DSH Sidebar kept exactly one **Memory System** row.
- Better Sidebar's **New tab** menu exposed exactly one **Memory System** item.
- Opening that item rendered the shared Mnemon UI with the target session and
  workspace scope. Status and Runtime navigation both loaded successfully.
- Reloading restored the Better Sidebar tab.
- A fresh browser tab reported zero console errors and zero warnings.
- Repeating the menu and render checks with Mnemon loaded before Better
  Sidebar also reported zero console errors and zero warnings.

## Screenshots

| Evidence | Capture |
| --- | --- |
| DSH's normal Memory System row and Better Sidebar's registered menu item are visible together | [Tab menu](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/better-sidebar-153/after-tab-menu.jpg) |
| The Memory System Runtime page is rendered inside Better Sidebar | [Memory tab](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/better-sidebar-153/after-memory-tab.jpg) |

Both captures are from the isolated Harness `0.1.1-rc.2` profile at its native
1280 × 720 browser viewport.
