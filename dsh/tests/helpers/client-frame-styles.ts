import { readFileSync } from 'node:fs'
import { URL as NodeURL } from 'node:url'
import { transform } from 'lightningcss'
import { presentationNamespace } from '../../tsdown.config.ts'

/** Apply the shipped class maps and viewport rules; JSDOM does not evaluate media queries. */
export function installClientFrameStyles(width: number, height = 900): () => void {
  const files = ['src/client/MnemonView.module.css', 'src/client/MnemonSidebarView.module.css',
    ...['runtime', 'documents', 'memory-spaces'].flatMap(source => ['page', 'sidebar'].map(kind => `plugins/dsh-mnemon-source-${source}/presentation/${kind}.module.css`))]
  const styles = files.map(filename => {
    const style = document.createElement('style')
    style.textContent = transform({ filename: presentationNamespace(filename),
      code: readFileSync(new NodeURL('../../' + filename, import.meta.url)), cssModules: { pattern: '[hash]_[local]' }, minify: true }).code.toString()
    document.head.append(style)
    function viewportRules(rules: CSSRuleList): string {
      return Array.from(rules).map(rule => {
        if (rule instanceof CSSStyleRule) return rule.cssText
        if (rule instanceof CSSMediaRule) {
          let supported = true
          const remaining = rule.conditionText.replace(/\((width|height)(<=|>=)(\d+)px\)/gu, (_match, axis: string, bound: string, limit: string) => {
            const value = axis === 'width' ? width : height
            supported &&= bound === '>=' ? value >= Number(limit) : value <= Number(limit)
            return ''
          }).replace(/\band\b|\s/gu, '')
          return supported && remaining === '' ? viewportRules(rule.cssRules) : ''
        }
        return ''
      }).join('\n')
    }
    style.textContent = viewportRules(style.sheet!.cssRules)
    return style
  })
  return () => { for (const style of styles) style.remove() }
}
