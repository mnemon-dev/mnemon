/** Brain outline shared by native, fallback and Better Sidebar navigation. */
const MEMORY_ICON_PATHS = [
  'M8 3.4C8 2.2 7.2 1.5 6.2 1.5C5.2 1.5 4.4 2.2 4.3 3.2C2.8 3.2 1.9 4.4 2.2 5.8C.9 6.8 1 8.8 2.4 9.6C2.1 11 3.1 12.2 4.5 12.2C4.8 14.7 8 14.9 8 12.3C8 14.9 11.2 14.7 11.5 12.2C12.9 12.2 13.9 11 13.6 9.6C15 8.8 15.1 6.8 13.8 5.8C14.1 4.4 13.2 3.2 11.7 3.2C11.6 2.2 10.8 1.5 9.8 1.5C8.8 1.5 8 2.2 8 3.4Z',
  'M8 3.4v8.9',
  'M4.3 3.2C4.2 4.1 4.7 4.8 5.5 5.1M11.7 3.2C11.8 4.1 11.3 4.8 10.5 5.1',
  'M2.4 9.6C3.2 10 4.4 9.7 4.8 8.8M13.6 9.6C12.8 10 11.6 9.7 11.2 8.8',
  'M4.5 12.2C4.4 11.2 4.9 10.5 5.7 10.2M11.5 12.2C11.6 11.2 11.1 10.5 10.3 10.2',
] as const

/** Attributes that let a host locate one particular rendering of the outline. */
interface MemoryIconMarker {
  'data-dsh-plugin'?: string
  'data-dsh-part'?: string
}

/** The brain outline for React surfaces; the surrounding control owns labels and state. */
export function MemoryIcon({ size, ...marker }: { size: number } & MemoryIconMarker): JSX.Element {
  return <svg {...marker} aria-hidden="true" viewBox="0 0 16 16" width={size} height={size} fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round">
    {MEMORY_ICON_PATHS.map(path => <path key={path} d={path} />)}
  </svg>
}

/** The same outline built with DOM APIs for the fallback sidebar entry, which renders outside React. */
export function createMemoryIcon(size: number): SVGSVGElement {
  const namespace = 'http://www.w3.org/2000/svg'
  const icon = document.createElementNS(namespace, 'svg')
  icon.setAttribute('viewBox', '0 0 16 16')
  icon.setAttribute('width', String(size))
  icon.setAttribute('height', String(size))
  icon.setAttribute('fill', 'none')
  icon.setAttribute('stroke', 'currentColor')
  icon.setAttribute('stroke-width', '1')
  icon.setAttribute('stroke-linecap', 'round')
  icon.setAttribute('stroke-linejoin', 'round')
  icon.setAttribute('aria-hidden', 'true')
  for (const data of MEMORY_ICON_PATHS) {
    const path = document.createElementNS(namespace, 'path')
    path.setAttribute('d', data)
    icon.append(path)
  }
  return icon
}
