import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const sidebarCss = readFileSync(new URL('../src/client/MnemonSidebarView.module.css', import.meta.url), 'utf8')
const workspaceCss = readFileSync(new URL('../src/client/MnemonWorkspace.module.css', import.meta.url), 'utf8')
const viewCss = readFileSync(new URL('../src/client/MnemonView.module.css', import.meta.url), 'utf8')
const runtimeCss = readFileSync(new URL('../plugins/dsh-mnemon-source-runtime/presentation/sidebar.module.css', import.meta.url), 'utf8')
const spacesCss = readFileSync(new URL('../plugins/dsh-mnemon-source-memory-spaces/presentation/sidebar.module.css', import.meta.url), 'utf8')

const sidebarSurface = 'var(--dsw-alias-bg-overlay, var(--dsw-alias-bg-base))'
// The pinned DSH sidebar; 0.2.0-rc.2 ships the same panel row rules.
const dshSidebar = readFileSync(new URL('../node_modules/@deepseek-ai/dsh-client-ui-sidebar/lib/client.js', import.meta.url), 'utf8')

/** One rule's declarations, with minifier spellings normalized. */
function declarations(rule: string): Map<string, string> {
  const spelled = (value: string) => value === '0 0' ? 'transparent' : value.replace(/,\s+/g, ',')
  return new Map(rule.split(';').map(part => part.trim()).filter(Boolean).map(part => {
    const colon = part.indexOf(':')
    return [part.slice(0, colon).trim(), spelled(part.slice(colon + 1).trim())] as [string, string]
  }))
}

describe('Sidebar layout invariants', () => {
  it('keeps the workspace surfaces opaque under transparent-base skins with a default-theme fallback', () => {
    expect(viewCss).toContain('--mn-bg: var(--dsw-alias-bg-base);')
    expect(viewCss).toContain('--mn-backdrop: var(--dsw-alias-bg-overlay, var(--mn-bg));')
    expect(viewCss).toContain('--mn-surface: linear-gradient(var(--mn-bg), var(--mn-bg)), linear-gradient(var(--mn-backdrop), var(--mn-backdrop)) var(--mn-bg);')
    expect(sidebarCss).toContain('.shell.shell {\n  background: var(--mn-surface);')
    expect(workspaceCss).toContain(`background: ${sidebarSurface};`)
  })

  it('keeps the sidebar artifact launcher-only and never hides DSH conversation content', () => {
    expect(workspaceCss).toContain('.entry {')
    expect(workspaceCss).toContain('.workspacePanel > * {')
    expect(workspaceCss).toContain('width: 100%;')
    expect(workspaceCss).toContain('flex: 1 1 auto;')
    expect(workspaceCss).not.toContain('[data-dsh-mnemon-view]')
    expect(workspaceCss).not.toContain('data-dsh-mnemon-active')
    expect(workspaceCss).not.toContain('.dshDesktopConversationSurface')
  })

  it('draws the fallback entry as DSH draws its own panel rows (#318)', () => {
    // DSH ships its sidebar CSS module minified inside the client bundle.
    const nativeRule = (selector: string) => declarations(new RegExp(`\\.[\\w-]+_${selector}\\{([^}]*)\\}`).exec(dshSidebar)?.[1] ?? '')
    const row = nativeRule('panelRow')
    const entry = declarations(/\.entry \{([^}]*)\}/.exec(workspaceCss)?.[1] ?? '')
    expect(row.size).toBeGreaterThan(10)
    for (const [property, value] of row) expect(entry.get(property), property).toBe(value)
    // The same highlight for hover and the open workspace, with no bold active label.
    expect(nativeRule('panelRow:hover').get('background')).toBe('var(--dsw-alias-interactive-bg-hover)')
    const highlight = declarations(/\.entry:hover,\n\.entry\[data-active\] \{([^}]*)\}/.exec(workspaceCss)?.[1] ?? '')
    expect(highlight.get('background')).toBe('var(--dsw-alias-interactive-bg-hover)')
    expect(workspaceCss).not.toMatch(/\.entry[^{]*\{[^}]*font-weight/)
    expect(declarations(/\.entry:focus-visible \{([^}]*)\}/.exec(workspaceCss)?.[1] ?? '')).toEqual(nativeRule('panelRow:focus-visible'))
    // Glyphs follow the native slot: 16px in the wide sidebar and 18px on the rail, with no wider box.
    expect(dshSidebar).toMatch(/size:\s*wide\s*\?\s*16\s*:\s*18/)
    expect(workspaceCss).toMatch(/\.entryIcon svg \{[^}]*width: 16px;[^}]*height: 16px;/)
    expect(workspaceCss).toMatch(/\[data-sidebar-collapsed\] \.entryIcon svg \{[^}]*width: 18px;[^}]*height: 18px;/)
    expect(/\.entryIcon \{([^}]*)\}/.exec(workspaceCss)?.[1]).not.toMatch(/width|height/)
    const rail = declarations(/\[data-sidebar-collapsed\] \.entry \{([^}]*)\}/.exec(workspaceCss)?.[1] ?? '')
    expect({ width: rail.get('width'), height: rail.get('height'), padding: rail.get('padding'), 'justify-content': rail.get('justify-content'), 'border-radius': rail.get('border-radius') })
      .toEqual({ width: '36px', height: '36px', padding: '0', 'justify-content': 'center', 'border-radius': undefined })
  })

  it('pins primary page headers at the canvas origin without an initial sticky settling distance', () => {
    expect(sidebarCss).toContain(".shell .canvas[data-lock-page-header] [class*='pageHeader'] {\n  position: sticky;\n  z-index: 12;\n  top: 0;")
  })

  it('keeps the connected label visible in the compact Sidebar header', () => {
    expect(sidebarCss).toContain(".shell .statusCluster > span:not([class*='statusDot']) { display: inline; }")
    expect(sidebarCss).toContain('.shell .headerActions { max-width: none; flex: 0 0 auto; }')
    expect(sidebarCss).toContain(".shell [class*='backButton'] > span { display: none; }")
  })

  it('keeps DSH as the only sidebar and preserves the established Mnemon tab strip', () => {
    expect(viewCss).toContain('.workspace { display: flex; min-height: 0; flex: 1; flex-direction: column; }')
    expect(viewCss).toContain('.topNavigation { display: flex;')
    expect(sidebarCss).toContain('.shell .topNavigation {\n  min-height: 0;')
    expect(sidebarCss).toContain('border-bottom-color: var(--dsw-alias-state-business-primary);')
  })

  it('renders runtime metadata as real chips while keeping form values at normal weight', () => {
    expect(runtimeCss).toContain(".shell [class*='runtimeEntryBadges'] > span {")
    expect(runtimeCss).toContain('border-radius: 999px;')
    expect(runtimeCss).toContain(".shell [class*='runtimeEntryBadges'] > [class*='runtimeEntryTarget'] {")
    expect(sidebarCss).toContain(".shell textarea { font-family: var(--dsw-font-family); font-size: 13px; font-weight: 400; }")
    expect(sidebarCss).toContain('.shell select { cursor: pointer; font-weight: 400; }')
  })

  it('keeps memory-space footer blocks aligned and safely truncatable', () => {
    expect(spacesCss).toContain(".shell [class*='bodyGrid'] {\n  grid-template-columns: repeat(auto-fit, minmax(min(320px, 100%), 1fr));")
    expect(spacesCss).toContain('grid-template-columns: minmax(0, 1fr) max-content;')
    expect(spacesCss).toContain(".shell .bodyCardFooter {\n  display: grid;")
    expect(spacesCss).toContain('white-space: nowrap;')
    expect(spacesCss).toContain(".shell .bodyCardStats {\n  display: flex;\n  min-width: 0;\n  flex-wrap: nowrap;")
    expect(spacesCss).toContain('  overflow: hidden;')
  })
})
