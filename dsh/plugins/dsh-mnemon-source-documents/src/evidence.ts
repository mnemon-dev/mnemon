import { truncateMemoryText } from 'dsh-mnemon/extension-sdk'

/** Preserve query-local evidence under the Source's per-document budget. */
export function documentEvidence(content: string, query: string, maximum: number): string {
  if (maximum <= 0) return ''
  if (content.length <= maximum) return content
  const normalized = content.toLocaleLowerCase()
  const terms = [query.trim(), ...(query.match(/[\p{L}\p{N}_-]+/gu) ?? [])]
    .map(term => term.toLocaleLowerCase()).filter(Boolean)
    .sort((left, right) => right.length - left.length)
  const matched = terms.map(term => normalized.indexOf(term)).find(index => index >= 0) ?? 0
  const projectedStart = Math.max(0, Math.min(content.length - maximum, matched - 400))
  const prefix = projectedStart === 0 ? '' : '[earlier content omitted]\n'
  const suffix = projectedStart + maximum >= content.length ? '' : '\n[later content omitted]'
  if (maximum <= prefix.length + suffix.length) return truncateMemoryText(content.slice(matched), maximum)
  const bodyLength = maximum - prefix.length - suffix.length
  let start = suffix === '' ? content.length - bodyLength : projectedStart
  if (/[\uDC00-\uDFFF]/u.test(content[start]!)) start++
  let end = Math.min(content.length, start + bodyLength)
  if (/[\uD800-\uDBFF]/u.test(content[end - 1]!)) end--
  return `${prefix}${content.slice(start, end)}${suffix}`
}
