export interface MnemonLogoProps {
  className?: string | undefined
  title?: string
}

/** Official Mnemon mark from mnemon-dev/mnemon (Apache-2.0). */
export function MnemonLogo({ className, title = 'Mnemon' }: MnemonLogoProps): JSX.Element {
  return (
    <svg className={className} xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 400" role="img" aria-label={title}>
      <rect width="400" height="400" fill="#1A1A1A" />
      <path d="M 91.5,153.5 L 98.5,146.5 L 98.5,98.5 L 146.5,98.5 L 153.5,91.5 L 91.5,91.5 Z" fill="#D4D4D8" />
      <path d="M 246.5,91.5 L 253.5,98.5 L 301.5,98.5 L 301.5,146.5 L 308.5,153.5 L 308.5,91.5 Z" fill="#D4D4D8" />
      <path d="M 91.5,246.5 L 98.5,253.5 L 98.5,301.5 L 146.5,301.5 L 153.5,308.5 L 91.5,308.5 Z" fill="#D4D4D8" />
      <path d="M 308.5,246.5 L 301.5,253.5 L 301.5,301.5 L 253.5,301.5 L 246.5,308.5 L 308.5,308.5 Z" fill="#D4D4D8" />
      <polyline points="265,187 278,200 265,213" fill="none" stroke="#D4D4D8" strokeWidth="2" strokeLinecap="square" />
      <polyline points="135,187 122,200 135,213" fill="none" stroke="#D4D4D8" strokeWidth="2" strokeLinecap="square" />
      <polygon points="200,155 245,200 200,245 155,200" fill="none" stroke="#D4D4D8" strokeWidth="7" />
    </svg>
  )
}
