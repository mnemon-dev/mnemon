import type { JSX } from 'react'
import type { MemoryProviderIcon, MemoryProviderId } from '../contracts.ts'
import { MnemonLogo } from 'dsh-mnemon/client'
export interface ProviderIconProps {
  providerId: MemoryProviderId
  icon?: MemoryProviderIcon | undefined
  className?: string | undefined
  title?: string | undefined
}

function GenericProviderMark(): JSX.Element {
  return <svg viewBox="0 0 36 36" fill="none" xmlns="http://www.w3.org/2000/svg">
    <rect width="36" height="36" rx="9" fill="#F1F3F7" />
    <ellipse cx="18" cy="11" rx="8" ry="3.5" fill="#68738A" />
    <path d="M10 11v7c0 1.93 3.58 3.5 8 3.5s8-1.57 8-3.5v-7M10 18v7c0 1.93 3.58 3.5 8 3.5s8-1.57 8-3.5v-7" stroke="#68738A" strokeWidth="1.6" />
  </svg>
}

/** Only the owning plugin supplies image data; unknown brands get a neutral mark. */
export function ProviderIcon({ providerId, icon, className, title }: ProviderIconProps): JSX.Element {
  const accessibility = title === undefined ? { 'aria-hidden': true as const } : { role: 'img' as const, 'aria-label': title }
  return <span className={className} data-provider-icon={providerId} {...accessibility}>{
    icon?.kind === 'brand' && icon.value === 'mnemon' ? <MnemonLogo />
      : icon?.kind === 'glyph' ? <span aria-hidden="true">{icon.value}</span>
      : icon?.kind === 'data-url' && /^data:image\/(?:png|jpeg|webp|svg\+xml);/u.test(icon.value) ? <img src={icon.value} alt="" />
      : <GenericProviderMark />
  }</span>
}
