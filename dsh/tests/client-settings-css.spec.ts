import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const settingsCss = readFileSync(new URL('../src/client/MnemonSettingsCard.module.css', import.meta.url), 'utf8')

describe('Settings layout invariants', () => {
  it('anchors the remaining visually hidden radios to their visible segments', () => {
    expect(settingsCss).toContain('.providerToggle { display: inline-flex; position: relative; cursor: pointer; }')
    expect(settingsCss).toContain('.inlineChoices label { position: relative; cursor: pointer; }')
    expect(settingsCss).toContain('.toggleRow {\n  display: flex;\n  position: relative;')
  })

  it('gives a component page the page\'s box model, though DSH renders the dialog outside the page', () => {
    expect(settingsCss).toContain(':where(.page, .surface) *, :where(.page, .surface) *::before, :where(.page, .surface) *::after { box-sizing: border-box; }')
    expect(settingsCss).toContain(':where(.page, .surface) :where(button, input, select, textarea) { color: inherit; font: inherit; }')
  })

  it('fits field grids to their width instead of forcing two columns', () => {
    expect(settingsCss).toContain('grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr))')
    expect(settingsCss).not.toContain('position: sticky')
  })

  it('keeps the composition controls compact in a host-constrained mobile column', () => {
    expect(settingsCss).toContain('.boardSection { container-type: inline-size; }')
    expect(settingsCss).toContain('@container (max-width: 180px)')
    expect(settingsCss).toContain('.boardSection .settingCopy small { display: none; }')
    expect(settingsCss).toContain('.boardSection .boardControl { justify-self: end; }')
  })
})
