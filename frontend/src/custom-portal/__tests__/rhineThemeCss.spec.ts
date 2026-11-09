// @vitest-environment node
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import postcss, { type Root, type Rule } from 'postcss'
import tailwind from 'tailwindcss'
import { beforeAll, describe, expect, it } from 'vitest'
import { DEFAULT_PALETTE, PALETTES, type PortalPalette } from '../theme/palettes'
import tailwindConfig from '../theme/tailwind.rhine.config.js'
import { contrastRatio, paletteTokens, resolveColor } from './contrast'

const themePath = fileURLToPath(new URL('../theme/rhine-theme.css', import.meta.url))
const ROOT = ':where(html.theme-rhine)'
const UPSTREAM_TEAL =
  /#(f0fdfa|ccfbf1|99f6e4|5eead4|2dd4bf|14b8a6|0d9488|0f766e|115e59|134e4a|042f2e)\b|\b20,\s*184,\s*166\b|\b20 184 166\b/i
const ALLOWED_PROPERTY =
  /^(--tw-[a-z-]+|--pp-[a-z0-9-]+|--color-primary-500|color|background(-color|-image|-size|-clip)?|border(-[a-z]+)*-(color|radius|width|style)|border-(color|radius|width|style)|border-image(-[a-z]+)?|box-shadow|fill|stroke|outline(-[a-z]+)?|accent-color|caret-color|text-decoration-color|font-variant-numeric|font-family|font-synthesis|letter-spacing|content|transform|transition|(-webkit-)?backdrop-filter)$/
const LAYOUT_PROPERTY =
  /^(display|position|float|clear|width|height|(min|max)-(width|height)|margin(-[a-z]+)?|padding(-[a-z]+)?|grid(-[a-z]+)*|flex(-[a-z]+)*|gap|row-gap|column-gap|order|top|right|bottom|left|inset|align-[a-z]+|justify-[a-z]+|place-[a-z]+|font-size|line-height)$/

let root: Root

function rules(): Rule[] {
  const found: Rule[] = []
  root.walkRules((rule) => {
    if (rule.parent?.type === 'atrule' && /keyframes$/.test((rule.parent as { name: string }).name)) return
    found.push(rule)
  })
  return found
}

const PAPER = DEFAULT_PALETTE.light
const NIGHT = DEFAULT_PALETTE.dark
const LIGHT = PALETTES.filter((palette) => palette.mode === 'light')
const DARK = PALETTES.filter((palette) => palette.mode === 'dark')

/** Resolves a compiled colour in `palette`; a translucent colour is composited over `under`. */
function solid(value: string, palette: PortalPalette = PAPER, under = 'var(--pp-surface)'): string {
  return resolveColor(value, paletteTokens(palette), under)
}

const inMedia = (rule: Rule) => rule.parent?.type === 'atrule' && (rule.parent as { name: string }).name === 'media'

/** Merges, in source order, every rule outside @media that lists `selector`, like the cascade would. */
function declarationsOf(selector: string): Record<string, string> {
  const declarations: Record<string, string> = {}
  for (const rule of rules().filter((candidate) => !inMedia(candidate) && candidate.selectors.includes(selector))) {
    rule.walkDecls((decl) => {
      declarations[decl.prop] = decl.value
    })
  }
  return declarations
}

describe('Rhine theme stylesheet', () => {
  beforeAll(async () => {
    const result = await postcss([tailwind()]).process(readFileSync(themePath, 'utf8'), { from: themePath })
    root = result.root
  }, 60_000)

  it('scopes every rule under the root class without adding specificity', () => {
    const unscoped = rules()
      .flatMap((rule) => rule.selectors)
      .filter((selector) => !selector.startsWith(ROOT))
    expect(unscoped).toEqual([])
  })

  it('only overrides colour, radius and shadow so upstream layout stays intact', () => {
    const properties = new Set<string>()
    // Arbitrary-property utilities such as `[overflow-wrap:anywhere]` are re-emitted
    // verbatim by Tailwind; their values match upstream, so they are not overrides.
    for (const rule of rules().filter((candidate) => !candidate.selector.includes(' .\\['))) {
      rule.walkDecls((decl) => {
        properties.add(decl.prop)
      })
    }
    expect([...properties].filter((prop) => !ALLOWED_PROPERTY.test(prop))).toEqual([])
  })

  it('re-emits primary utilities with the Rhine palette', () => {
    expect(declarationsOf(`${ROOT} .bg-primary-600`)['background-color']).toBe('rgb(var(--pp-c-primary-600) / 1)')
    expect(solid(declarationsOf(`${ROOT} .bg-primary-600`)['background-color'])).toBe('#58724f')
    expect(solid(declarationsOf(`${ROOT} .dark\\:text-primary-400:is(.dark *)`).color, NIGHT)).toBe('#a3bca0')
    expect(declarationsOf(`${ROOT} .rounded-xl`)['border-radius']).toBe('0px')
    expect(declarationsOf(`${ROOT} .rounded-full`)['border-radius']).toBe('0px')
    const tealPrimary = rules().filter(
      (rule) => /primary/.test(rule.selector) && /#(14b8a6|0d9488|0f766e|2dd4bf)/i.test(rule.toString())
    )
    expect(tealPrimary.map((rule) => rule.selector)).toEqual([])
  })

  it('clears the button shadow through --tw-shadow so upstream .btn:focus keeps only the ring', () => {
    // Upstream `.btn:focus` outranks `.btn-primary` and sets box-shadow from the
    // ring plus var(--tw-shadow), so the theme clears the shadow through it.
    const button = declarationsOf(`${ROOT} .btn`)
    expect(button['--tw-shadow']).toBe('0 0 #0000')
    expect(button['box-shadow']).toMatch(/var\(--tw-shadow\)$/)
    expect(declarationsOf(`${ROOT} .btn:focus`)['--tw-ring-color']).toMatch(/^rgb\(var\(--pp-c-primary-500\) \//)
  })

  it('draws no drop shadows and moves nothing on hover or press', () => {
    const offsetShadows: string[] = []
    const moves: string[] = []
    const arbitraryShadow = (selector: string) => selector.includes('shadow-\\[')
    // Arbitrary `shadow-[…]` utilities are re-emitted verbatim; a later rule empties their variables.
    for (const rule of rules().filter((candidate) => !arbitraryShadow(candidate.selector))) {
      rule.walkDecls((decl) => {
        if (/shadow$/.test(decl.prop) && /\b[1-9]\d*px [1-9]\d*px\b/.test(decl.value)) offsetShadows.push(rule.selector)
        if (decl.prop === 'transform') moves.push(rule.selector)
      })
    }
    expect(offsetShadows).toEqual([])
    const all = rules()
    const neutraliser = all.findIndex((rule) => rule.selector === `${ROOT} [class*='shadow-[']`)
    expect(declarationsOf(`${ROOT} [class*='shadow-[']`)['--tw-shadow']).toBe('0 0 #0000')
    expect(neutraliser).toBeGreaterThan(all.findLastIndex((rule) => arbitraryShadow(rule.selector)))
    expect(moves).toEqual([])
    expect(declarationsOf(ROOT)['--pp-shadow']).toBeUndefined()
  })

  it('inverts a pressed button to ink instead of pushing it down', () => {
    const pressed = declarationsOf(`${ROOT} .btn:active:not(:disabled)`)
    expect(solid(pressed['background-color'])).toBe('#2c2e33')
    expect(solid(pressed.color)).toBe('#f8f7f2')
    const dark = declarationsOf(`${ROOT} .btn:active:not(:disabled):is(.dark *)`)
    for (const [palettes, box] of [[LIGHT, pressed], [DARK, dark]] as const) {
      for (const palette of palettes) {
        expect(contrastRatio(solid(box.color, palette), solid(box['background-color'], palette)), palette.id).toBeGreaterThanOrEqual(4.5)
      }
    }
  })

  it('overrides upstream component classes for light and dark modes', () => {
    expect(solid(declarationsOf(`${ROOT} .btn-primary`)['background-color'])).toBe('#8fa888')
    expect(solid(declarationsOf(`${ROOT} .btn-primary`).color)).toBe('#2c2e33')
    expect(solid(declarationsOf(`${ROOT} .btn-primary:is(.dark *)`)['background-color'], NIGHT)).toBe('#a3bca0')
    expect(solid(declarationsOf(`${ROOT} .card:is(.dark *)`)['background-color'], NIGHT)).toBe('#232630')
    expect(declarationsOf(`${ROOT} .sidebar-link-active`)['box-shadow']).toBe('inset 3px 0 0 var(--pp-ink)')
  })

  it('leaves no upstream brand teal outside categorical and arbitrary utilities', () => {
    // `teal-*`/`cyan-*` utilities are platform identity colours and arbitrary
    // utilities are re-emitted verbatim; both are handled separately.
    const leaks = rules().filter(
      (rule) => !/teal|cyan|\\\[/.test(rule.selector) && UPSTREAM_TEAL.test(rule.nodes.join(';'))
    )
    expect(leaks.map((rule) => rule.selector)).toEqual([])
    expect(declarationsOf(`${ROOT} .bg-mesh-gradient`)['background-image']).toMatch(/var\(--pp-grid\) 0 1px, transparent 1px 16px/)
  })

  it('ties scoped upstream component styles by repeating the class', () => {
    const red = (tailwindConfig.theme.extend.colors as Record<string, Record<string, string>>).red['500']
    expect(solid(declarationsOf(`${ROOT} .select-trigger.select-trigger:focus`)['border-color'])).toBe('#2c2e33')
    expect(solid(declarationsOf(`${ROOT} .select-trigger-open.select-trigger-open`)['border-color'])).toBe('#2c2e33')
    expect(solid(declarationsOf(`${ROOT} .select-trigger-error.select-trigger-error:focus`)['border-color'])).toBe(red)
    // The dark focus override is (0,4,0), so the error state needs its own dark variant.
    expect(
      solid(declarationsOf(`${ROOT} .select-trigger-error.select-trigger-error:focus:is(.dark *)`)['border-color'])
    ).toBe(red)
    // Upstream scoped `dark:bg-dark-800` is fixed slate; the tie repaints it with the palette.
    for (const control of ['.select-trigger', '.date-picker-trigger', '.select-dropdown-portal', '.date-picker-dropdown']) {
      const selector = `${ROOT} ${control}${control}:is(.dark *)`
      expect(declarationsOf(selector)['background-color'], control).toBe('rgb(var(--pp-c-dark-800) / 1)')
    }
    expect(solid(declarationsOf(`${ROOT} .select-trigger-disabled.select-trigger-disabled`)['background-color'])).toBe(
      DEFAULT_PALETTE.light.gray[100]
    )
    expect(declarationsOf(ROOT)['--color-primary-500']).toBe('var(--pp-c-primary-500)')
    expect(solid(declarationsOf(`${ROOT} .select-option-selected.select-option-selected`).color)).toBe('#364a31')
    expect(declarationsOf(`${ROOT} .settings-tab-active.settings-tab-active::after`)['background-color']).toBe('rgb(var(--pp-c-primary-500))')
  })

  it('repaints upstream fixed-slate dark borders, icons and slate utilities with the palette', () => {
    const dark = `${ROOT}.dark`
    expect(declarationsOf(`${dark} .input`)['border-color']).toBe('var(--pp-control)')
    expect(declarationsOf(`${dark} .btn-secondary.btn-secondary`)['border-color']).toBe('var(--pp-edge)')
    expect(declarationsOf(`${dark} .portal-shell .table-scroll-container.table-scroll-container`)['border-color']).toBe(
      'var(--pp-edge)'
    )
    for (const control of ['.select-trigger', '.date-picker-trigger', '.date-picker-input']) {
      expect(declarationsOf(`${dark} ${control}${control}`)['border-color'], control).toBe('var(--pp-control)')
    }
    for (const icon of ['.select-icon', '.date-picker-icon', '.date-picker-chevron']) {
      expect(declarationsOf(`${dark} ${icon}${icon}`).color, icon).toBe('rgb(var(--pp-c-dark-400))')
    }
    expect(declarationsOf(`${ROOT} .dark\\:bg-slate-900:is(.dark *)`)['background-color']).toBe(
      'rgb(var(--pp-c-dark-900) / 1)'
    )
  })

  it('places utility replacements after the re-emitted utilities', () => {
    const all = rules()
    const lastIndex = (selector: string) => all.findLastIndex((rule) => rule.selectors.includes(selector))
    const placeholder = `${ROOT} .placeholder\\:text-gray-400::placeholder`
    expect(solid(declarationsOf(placeholder).color)).toBe('#686c62')
    const grid = all.findIndex((rule) => rule.selector.startsWith(`${ROOT} [class*='rgba(20,184,166,0.03)']`))
    const arbitraryGrid = all.findIndex((rule) => rule.selector.includes('\\[linear-gradient') && rule.selector.includes('184'))
    expect(grid).toBeGreaterThan(-1)
    expect(arbitraryGrid === -1 || grid > arbitraryGrid).toBe(true)
    expect(lastIndex(placeholder)).toBeGreaterThan(all.findIndex((rule) => rule.selector === `${ROOT} .bg-primary-600`))
  })

  it('never changes layout, so upstream grids and spacing stay put', () => {
    const layout: string[] = []
    for (const rule of rules().filter((candidate) => !candidate.selector.includes(' .\\['))) {
      rule.walkDecls((decl) => {
        if (LAYOUT_PROPERTY.test(decl.prop)) layout.push(`${rule.selector} { ${decl.prop} }`)
      })
    }
    expect(layout).toEqual([])
  })

  it('draws the canvas as a 16px grid that scrolls with the page', () => {
    // The dark rule repeats the canvas because upstream `dark:bg-dark-950` outranks the base one.
    for (const selector of [`${ROOT} body`, `${ROOT}.dark body`]) {
      const body = declarationsOf(selector)
      expect(body['background-image'], selector).toMatch(/var\(--pp-grid\) 0 1px, transparent 1px 16px/)
      expect(body['background-color'], selector).toBe('var(--pp-canvas)')
      expect(Object.values(body).join(' '), selector).not.toMatch(/\bfixed\b/)
    }
  })

  it('frames surfaces with three rules and controls with two', () => {
    expect(declarationsOf(`${ROOT} .card`)['border-width']).toBe('5px')
    expect(declarationsOf(`${ROOT} .card`)['box-shadow']).toBe('none')
    expect(declarationsOf(`${ROOT} .glass-card`)['backdrop-filter']).toBe('none')
    expect(declarationsOf(`${ROOT} .glass-card`)['background-color']).toBe('#fff')
    for (const control of ['.btn', '.input', '.badge', '.select-trigger.select-trigger']) {
      const box = declarationsOf(`${ROOT} ${control}`)
      // A 3px double border is two 1px rules with a 1px gap and still takes border-color.
      expect([box['border-width'], box['border-style']], control).toEqual(['3px', 'double'])
    }
    expect(declarationsOf(`${ROOT} .input`)['border-color']).toBe('var(--pp-control)')
    // Forced-colors mode drops box-shadow, so focus must be a real outline.
    expect(declarationsOf(`${ROOT} .input:focus-visible`).outline).toBe('2px solid var(--pp-ink)')
    expect(declarationsOf(`${ROOT} .select-trigger.select-trigger`)['border-radius']).toBe('0')
    expect(declarationsOf(`${ROOT} .table th`)['border-bottom-width']).toBe('2px')
    expect(declarationsOf(`${ROOT} .page-title`)['font-family']).toBe('var(--pp-font-pixel)')
    expect(declarationsOf(`${ROOT} .stat-value`)['font-family']).toBe('var(--pp-font-pixel)')
  })

  it('cancels button motion when reduced motion is preferred', () => {
    const reduced: Rule[] = []
    root.walkAtRules('media', (media) => {
      if (/prefers-reduced-motion:\s*reduce/.test(media.params)) media.walkRules((rule) => void reduced.push(rule))
    })
    const value = (selector: string, prop: string) => {
      let found: string | undefined
      reduced
        .filter((rule) => rule.selectors.includes(selector))
        .forEach((rule) => rule.walkDecls(prop, (decl) => void (found = decl.value)))
      return found
    }
    expect(value(`${ROOT} .btn`, 'transition')).toBe('none')
  })

  describe('pixel cues', () => {
    const mediaDeclarations = (query: RegExp, selector: string) => {
      const found: Record<string, string> = {}
      root.walkAtRules('media', (media) => {
        if (!query.test(media.params)) return
        media.walkRules((rule) => {
          if (rule.selectors.includes(selector)) rule.walkDecls((decl) => void (found[decl.prop] = decl.value))
        })
      })
      return found
    }
    const framed = [
      `${ROOT} .card`,
      `${ROOT} .glass-card`,
      `${ROOT} .modal-content`,
      `${ROOT} .dropdown`,
      `${ROOT} .toast`,
      `${ROOT} .table-container`
    ]
    const FRAME_RULES = 'M0 0h11v11H0zM1 1v9h9V1zM2 2h7v7H2zM3 3v5h5V3zM4 4h3v3H4zM5 5v1h1V5z'

    it('draws three 1px rules 1px apart in the edge colour of each palette', () => {
      for (const palette of PALETTES) {
        const vars = paletteTokens(palette)
        const svg = decodeURIComponent(vars['--pp-frame'])
        expect(svg, palette.id).toMatch(/^url\("data:image\/svg\+xml,<svg /)
        expect(svg, palette.id).toContain(`fill='${vars['--pp-edge']}'`)
        expect(svg, palette.id).toContain(`fill-rule='evenodd' d='${FRAME_RULES}'`)
      }
      for (const selector of framed) {
        const box = declarationsOf(selector)
        expect(box['border-width'], selector).toBe('5px')
        expect(box['border-image'], selector).toBe('var(--pp-frame) 5 / 5px')
        // The gaps between the rules show the surface's own fill.
        expect(box['background-clip'], selector).toBe('border-box')
      }
    })

    it('keeps state borders and forced colours visible where the frame image would hide them', () => {
      expect(declarationsOf(`${ROOT} .card[class*='border-primary']`)['border-image-source']).toBe('none')
      expect(declarationsOf(`${ROOT} .card-hover:hover`)['border-image-source']).toBe('none')
      for (const selector of framed) {
        expect(mediaDeclarations(/forced-colors:\s*active/, selector)['border-image-source'], selector).toBe('none')
      }
    })

    it('changes button fills in steps rather than easing', () => {
      const transition = declarationsOf(`${ROOT} .btn`).transition
      expect(transition.split(',').every((part) => /steps\(2\)/.test(part))).toBe(true)
    })

    it.each(['.progress-bar', '.table th', '.skeleton'])('dithers %s with a 2px checkerboard', (selector) => {
      const fill = declarationsOf(`${ROOT} ${selector}`)
      expect(fill['background-image']).toMatch(/^repeating-conic-gradient\(/)
      expect(fill['background-size']).toBe('4px 4px')
    })

    it.each(PALETTES.map((palette) => [palette.id, palette] as const))(
      'keeps table header text readable on the darkest dither cell in %s',
      (_, palette) => {
        const th = declarationsOf(`${ROOT} .table th${palette.mode === 'dark' ? ':is(.dark *)' : ''}`)
        expect(declarationsOf(`${ROOT} .table th`)['background-image']).toBe(
          'repeating-conic-gradient(var(--pp-dither) 0 25%, transparent 0 50%)'
        )
        const cell = solid('var(--pp-dither)', palette, solid(th['background-color'], palette))
        expect(contrastRatio(solid(th.color, palette), cell)).toBeGreaterThanOrEqual(4.5)
      }
    )

    it('squares scrollbar thumbs', () => {
      expect(declarationsOf(`${ROOT} ::-webkit-scrollbar-thumb`)['border-radius']).toBe('0')
    })
  })

  describe('contrast', () => {
    const color = (selector: string, prop = 'color') => declarationsOf(selector)[prop]
    const darkCard = (palette: PortalPalette) => solid(color(`${ROOT} .card:is(.dark *)`, 'background-color'), palette)

    it('keeps primary buttons readable in every palette', () => {
      for (const [palettes, selector] of [[LIGHT, `${ROOT} .btn-primary`], [DARK, `${ROOT} .btn-primary:is(.dark *)`]] as const) {
        const button = declarationsOf(selector)
        for (const palette of palettes) {
          const ratio = contrastRatio(solid(button.color, palette), solid(button['background-color'], palette))
          expect(ratio, palette.id).toBeGreaterThanOrEqual(4.5)
        }
      }
    })

    it('keeps control borders at 3:1 against their surface (WCAG 1.4.11)', () => {
      for (const palette of LIGHT) expect(contrastRatio(solid('var(--pp-control)', palette), '#ffffff'), palette.id).toBeGreaterThanOrEqual(3)
      for (const palette of DARK) {
        expect(contrastRatio(solid('var(--pp-control)', palette), darkCard(palette)), palette.id).toBeGreaterThanOrEqual(3)
      }
    })

    it.each(['primary', 'blue', 'amber', 'violet'])('keeps %s metric tones at 4.5:1', (hue) => {
      for (const palette of LIGHT) {
        const lightBg = solid(color(`${ROOT} .bg-${hue}-50`, 'background-color'), palette)
        expect(contrastRatio(solid(color(`${ROOT} .text-${hue}-600`), palette), lightBg), palette.id).toBeGreaterThanOrEqual(4.5)
      }
      for (const palette of DARK) {
        const darkBg = solid(color(`${ROOT} .dark\\:bg-${hue}-900\\/20:is(.dark *)`, 'background-color'), palette, darkCard(palette))
        const text = solid(color(`${ROOT} .dark\\:text-${hue}-400:is(.dark *)`), palette)
        expect(contrastRatio(text, darkBg), palette.id).toBeGreaterThanOrEqual(4.5)
      }
    })

    it('keeps secondary text at 4.5:1 on the canvas and cards', () => {
      for (const palette of LIGHT) {
        const text = solid(color(`${ROOT} .text-gray-500`), palette)
        for (const background of ['var(--pp-surface)', 'var(--pp-canvas)']) {
          expect(contrastRatio(text, solid(background, palette)), `${palette.id} ${background}`).toBeGreaterThanOrEqual(4.5)
        }
      }
      for (const palette of DARK) {
        const text = solid(color(`${ROOT} .dark\\:text-dark-400:is(.dark *)`), palette)
        for (const background of [darkCard(palette), solid('var(--pp-canvas)', palette)]) {
          expect(contrastRatio(text, background), `${palette.id} ${background}`).toBeGreaterThanOrEqual(4.5)
        }
      }
    })
  })
})
