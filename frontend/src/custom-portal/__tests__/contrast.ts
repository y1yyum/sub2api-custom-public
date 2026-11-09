import postcss from 'postcss'
import { DEFAULT_PALETTE, paletteVariables, type PortalPalette } from '../theme/palettes'

function channel(value: number): number {
  const srgb = value / 255
  return srgb <= 0.03928 ? srgb / 12.92 : ((srgb + 0.055) / 1.055) ** 2.4
}

function luminance(hex: string): number {
  const match = /^#?([0-9a-f]{6}|[0-9a-f]{3})$/i.exec(hex.trim())
  if (!match) throw new Error(`Not an opaque hex colour: ${hex}`)
  const digits = match[1].length === 3 ? [...match[1]].map((d) => d + d).join('') : match[1]
  const [r, g, b] = [0, 2, 4].map((i) => channel(parseInt(digits.slice(i, i + 2), 16)))
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

/** WCAG 2.x contrast ratio between two opaque hex colours. */
export function contrastRatio(foreground: string, background: string): number {
  const [light, dark] = [luminance(foreground), luminance(background)].sort((a, b) => b - a)
  return (light + 0.05) / (dark + 0.05)
}

/** Declarations of every rule listing `selector`, merged in source order like the cascade. */
export function declarationsIn(css: string, selector: string): Record<string, string> {
  const declarations: Record<string, string> = {}
  postcss.parse(css).walkRules((rule) => {
    if (!rule.selectors.includes(selector)) return
    rule.walkDecls((decl) => {
      declarations[decl.prop] = decl.value
    })
  })
  return declarations
}

export function scopedStyles(source: string): string {
  return [...source.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n')
}

/** Custom properties a palette sets on <html> (theme/palettes.ts). */
export function paletteTokens(palette: PortalPalette = DEFAULT_PALETTE.light): Record<string, string> {
  return Object.fromEntries(paletteVariables(palette))
}

/**
 * Resolves `var(--x)` against `tokens` and returns an opaque hex; `rgb(r g b / a)`
 * and `rgba()` are composited over `under`.
 */
export function resolveColor(value: string, tokens: Record<string, string>, under = '#ffffff'): string {
  let resolved = value.trim()
  for (let depth = 0; /var\(/.test(resolved); depth++) {
    if (depth > 8) throw new Error(`Unresolved colour: ${value}`)
    resolved = resolved.replace(/var\((--[\w-]+)(?:,\s*([^()]*(?:\([^()]*\))?[^()]*))?\)/g, (_, name: string, fallback?: string) => {
      const token = tokens[name] ?? fallback
      if (token === undefined) throw new Error(`Unknown token ${name} in ${value}`)
      return token.trim()
    })
  }
  if (/^#[0-9a-f]{6}$/i.test(resolved)) return resolved.toLowerCase()
  if (/^#fff$/i.test(resolved)) return '#ffffff'
  const rgb = /^rgba?\((\d+)[ ,]+(\d+)[ ,]+(\d+)(?:\s*[/,]\s*([\d.]+))?\)$/.exec(resolved)
  if (!rgb) throw new Error(`Unparsed colour: ${value} → ${resolved}`)
  const alpha = rgb[4] === undefined ? 1 : Number(rgb[4])
  const base = [1, 3, 5].map((i) => parseInt(resolveColor(under, tokens).slice(i, i + 2), 16))
  return `#${[1, 2, 3]
    .map((i, k) => Math.round(Number(rgb[i]) * alpha + base[k] * (1 - alpha)).toString(16).padStart(2, '0'))
    .join('')}`
}
