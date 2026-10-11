import type { JSX, ReactNode } from 'react'
import css from './MnemonSettingsCard.module.css'

interface GlobalLocationSettingProps {
  name: string
  ariaLabel: string
  label: string
  hint: ReactNode
  defaultLabel: string
  customLabel: string
  custom: boolean
  workspace: boolean
  disabled: boolean
  className?: string | undefined
  children?: ReactNode
  onChange: (custom: boolean) => void
  onInteract?: (() => void) | undefined
}

/** The default or custom location control: the storage directory, Native and workspace-aware providers. */
export function GlobalLocationSetting(props: GlobalLocationSettingProps): JSX.Element {
  return <div className={`${css.globalLocationSetting}${props.className === undefined ? '' : ` ${props.className}`}`}>
    <div className={css.nativeLocation}>
      <div className={css.settingCopy}><strong>{props.label}</strong>{props.hint !== '' && <small>{props.hint}</small>}</div>
      <div className={css.inlineChoices} role="radiogroup" aria-label={props.ariaLabel}>
        <label><input type="radio" name={props.name} checked={props.workspace || !props.custom} disabled={props.disabled || props.workspace} onClick={props.onInteract} onChange={() => props.onChange(false)} /><span>{props.defaultLabel}</span></label>
        <label><input type="radio" name={props.name} checked={!props.workspace && props.custom} disabled={props.disabled || props.workspace} onClick={props.onInteract} onChange={() => props.onChange(true)} /><span>{props.customLabel}</span></label>
      </div>
    </div>
    {!props.workspace && props.custom ? props.children : null}
  </div>
}
