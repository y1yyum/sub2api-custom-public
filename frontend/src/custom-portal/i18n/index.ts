import { useI18n } from 'vue-i18n'
import zh from './zh'
import en from './en'

export type PortalLocale = 'zh' | 'en'
export type PortalMessages = typeof zh

export const portalMessages: Record<PortalLocale, PortalMessages> = { zh, en }

/**
 * Upstream `loadLocaleMessages` replaces whole locales via `setLocaleMessage`,
 * which would discard anything merged into the global scope. Portal copy
 * therefore lives in a component-local scope that follows the global locale.
 *
 * The translator is exposed as `pt` because upstream's locale completeness
 * check treats every string-keyed `t()` call in src/ as a global locale key.
 */
export function usePortalI18n() {
  const { t, locale } = useI18n({ useScope: 'local', inheritLocale: true, messages: portalMessages })
  return { pt: t, locale }
}

export function resolvePortalLocale(locale: string): PortalLocale {
  return locale === 'zh' ? 'zh' : 'en'
}
