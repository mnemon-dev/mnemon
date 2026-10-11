import { createHash } from 'node:crypto'
import { transform } from 'lightningcss'

function hash(value: unknown): string {
  return createHash('sha256').update(JSON.stringify(value)).digest('hex')
}

function semantic(value: unknown): unknown {
  return JSON.parse(JSON.stringify(value, (key, item: unknown) => key === 'loc' ? undefined : item))
}

/** Compare selectors/declarations under their media/container conditions, not CSS formatting. */
export function presentationFingerprint(files: Array<{ filename: string; text: string }>) {
  const rules: string[] = []
  const classes: Record<string, string> = {}
  function visit(nodes: unknown[], parents: unknown[] = []) {
    for (const node of nodes) {
      const { type, value } = node as { type: string; value: Record<string, unknown> }
      if (type === 'style') {
        for (const selector of value.selectors as unknown[]) rules.push(JSON.stringify(semantic({ parents, selector, declarations: value.declarations })))
      } else if (Array.isArray(value.rules)) {
        const { rules: children, loc: _location, ...condition } = value
        visit(children as unknown[], [...parents, { type, condition }])
      } else rules.push(JSON.stringify(semantic({ parents, type, value })))
    }
  }
  for (const { filename, text } of files) {
    const compiled = transform({ filename, code: Buffer.from(text), cssModules: { pattern: '[hash]_[local]' }, minify: true,
      visitor: { StyleSheet(sheet) { visit(sheet.rules) } } })
    for (const [key, value] of Object.entries(compiled.exports ?? {})) {
      if (classes[key] !== undefined && classes[key] !== value.name) throw new Error('Conflicting page-kit class: ' + key)
      classes[key] = value.name
    }
  }
  return { rules: rules.length, rulesSha256: hash(rules.sort()), classes: Object.keys(classes).length,
    classesSha256: hash(Object.entries(classes).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0)) }
}

export function copyFingerprint(dictionary: Record<string, string>) {
  return { keys: Object.keys(dictionary).length, sha256: hash(Object.entries(dictionary).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0)) }
}
