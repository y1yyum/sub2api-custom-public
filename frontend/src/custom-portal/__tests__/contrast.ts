import postcss from 'postcss'

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
