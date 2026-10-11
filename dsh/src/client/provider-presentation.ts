import type { MemoryProviderConfigField, MemoryProviderDescriptor } from '../host/protocol.ts'
import type { MnemonKey, MnemonTranslate } from './locales.ts'

export function providerSummary(t: MnemonTranslate, provider: MemoryProviderDescriptor): string {
  if (provider.summaryI18nKey === undefined) return provider.summary
  const localized = t(provider.summaryI18nKey as MnemonKey)
  return typeof localized !== 'string' || localized.length === 0 || localized === provider.summaryI18nKey ? provider.summary : localized
}

export function providerFieldLabel(t: MnemonTranslate, field: MemoryProviderConfigField): string {
  if (field.i18nKey === undefined) return field.label
  const localized = t(field.i18nKey as MnemonKey)
  return typeof localized !== 'string' || localized.length === 0 || localized === field.i18nKey ? field.label : localized
}

export function providerOptionLabel(t: MnemonTranslate, option: NonNullable<MemoryProviderConfigField['options']>[number]): string {
  if (option.i18nKey === undefined) return option.label
  const localized = t(option.i18nKey as MnemonKey)
  return typeof localized !== 'string' || localized.length === 0 || localized === option.i18nKey ? option.label : localized
}
