import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PixelIcon from '../components/common/PixelIcon.vue'
import { PIXEL_ICONS, pixelRuns, type PixelIconName } from '../theme/pixelIcons'
import { declarationsIn } from './contrast'

// A ?raw CSS import is emptied under jsdom, so read the file directly.
const themeCss = readFileSync(resolve(__dirname, '../theme/rhine-theme.css'), 'utf8')
const names = Object.keys(PIXEL_ICONS) as PixelIconName[]

const sources = Object.entries(
  import.meta.glob('../**/*.{ts,vue}', { query: '?raw', import: 'default', eager: true }) as Record<string, string>
).filter(([path]) => !path.startsWith('./') && !path.includes('/__tests__/') && !path.endsWith('/theme/pixelIcons.ts'))

function usedIconNames(): Set<string> {
  const used = new Set<string>()
  for (const [, source] of sources) {
    for (const [, name] of source.matchAll(/<PixelIcon\b[^>]*?\sname="(\w+)"/g)) used.add(name)
    for (const [, binding] of source.matchAll(/<PixelIcon\b[^>]*?\s:name="([^"]+)"/g)) {
      for (const [, name] of binding.matchAll(/'(\w+)'/g)) used.add(name)
    }
    for (const [, name] of source.matchAll(/\bicon: '(\w+)'/g)) used.add(name)
  }
  return used
}

describe('pixel icons', () => {
  afterEach(() => vi.restoreAllMocks())

  it.each(names)('draws %s on a 16×16 grid with known characters only', (name) => {
    const { rows, tones } = PIXEL_ICONS[name]
    expect(rows).toHaveLength(16)
    for (const row of rows) expect(row).toMatch(/^[.#omx]{16}$/)
    const fills = new Set(rows.join('').replace(/[.#]/g, ''))
    expect(fills.size, 'more fill letters than the 3 tone slots').toBeLessThanOrEqual(3)
    expect(tones.length).toBeLessThanOrEqual(3)
  })

  it('covers every icon name the portal uses, so nothing falls back to the line icons', () => {
    const used = usedIconNames()
    expect(used.size).toBeGreaterThan(20)
    expect([...used].filter((name) => !(name in PIXEL_ICONS))).toEqual([])
  })

  it('no longer renders the upstream line icon directly anywhere in the portal', () => {
    const direct = sources.filter(([path, source]) => !path.endsWith('PixelIcon.vue') && /<Icon\b/.test(source))
    expect(direct.map(([path]) => path)).toEqual([])
  })

  it('defines every fill tone for both light and dark mode', () => {
    const light = declarationsIn(themeCss, ':where(html.theme-rhine)')
    const dark = declarationsIn(themeCss, ':where(html.theme-rhine).dark')
    const tones = new Set(names.flatMap((name) => [...PIXEL_ICONS[name].tones]))
    for (const tone of [...tones, 'card']) {
      expect(light[`--pp-tone-${tone}`], tone).toMatch(/^#[0-9a-f]{6}$/)
      expect(dark[`--pp-tone-${tone}`], tone).toMatch(/^#[0-9a-f]{6}$/)
    }
  })

  it('merges runs of one fill on a row into a single rect', () => {
    expect(pixelRuns('home')).toHaveLength(47)
    expect(pixelRuns('more')).toEqual([
      { x: 2, y: 7, width: 2, fill: 'currentColor' },
      { x: 7, y: 7, width: 2, fill: 'currentColor' },
      { x: 12, y: 7, width: 2, fill: 'currentColor' },
      { x: 2, y: 8, width: 2, fill: 'currentColor' },
      { x: 7, y: 8, width: 2, fill: 'currentColor' },
      { x: 12, y: 8, width: 2, fill: 'currentColor' }
    ])
    expect(pixelRuns('cube').map((run) => run.fill)).toEqual(
      expect.arrayContaining(['var(--pp-tone-peach)', 'var(--pp-tone-peach-mid)', 'var(--pp-tone-peach-ink)'])
    )
  })

  it('renders crisp rects at whole multiples of 16px with the ink in currentColor', () => {
    const sizes = { xs: 'h-4 w-4', sm: 'h-4 w-4', md: 'h-8 w-8', lg: 'h-12 w-12' } as const
    for (const [size, classes] of Object.entries(sizes)) {
      const svg = mount(PixelIcon, { props: { name: 'home', size: size as keyof typeof sizes } }).find('svg')
      expect(svg.classes()).toEqual(classes.split(' '))
      expect(svg.attributes()).toMatchObject({ viewBox: '0 0 16 16', 'shape-rendering': 'crispEdges', 'aria-hidden': 'true' })
      expect(svg.findAll('rect')).toHaveLength(pixelRuns('home').length)
    }
  })

  it('falls back to the upstream icon for a name without pixel art and warns once', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const first = mount(PixelIcon, { props: { name: 'play', size: 'sm' } })
    mount(PixelIcon, { props: { name: 'play' } })
    expect(first.find('svg').attributes('data-pixel-icon')).toBeUndefined()
    expect(first.find('path').exists()).toBe(true)
    expect(warn).toHaveBeenCalledTimes(1)
  })
})
