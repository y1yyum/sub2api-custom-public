// @vitest-environment node
import { createHash } from 'node:crypto'
import { describe, expect, it } from 'vitest'
import postcss from 'postcss'
import dockSource from '../shell/PortalDock.vue?raw'
import topBarSource from '../shell/PortalTopBar.vue?raw'
import { contrastRatio, declarationsIn, scopedStyles } from './contrast'
import tailwindConfig from '../theme/tailwind.rhine.config.js'

const LAYOUT_PROPERTY =
  /^(display|position|float|width|height|(min|max)-(width|height)|margin(-[a-z]+)?|padding(-[a-z]+)?|grid(-[a-z]+)*|flex(-[a-z]+)*|gap|order|top|right|bottom|left|inset|align-[a-z]+|justify-[a-z]+|line-height)$/

const templateHash = (source: string) =>
  createHash('sha256')
    .update(source.replace(/\r\n/g, '\n').match(/^<template>[\s\S]*?^<\/template>$/m)![0])
    .digest('hex')

function fontSizes(css: string, selector: string): string[] {
  const sizes: string[] = []
  postcss.parse(css).walkRules((rule) => {
    if (rule.selectors.includes(selector)) rule.walkDecls('font-size', (decl) => void sizes.push(decl.value))
  })
  return sizes
}

function layoutDeclarations(css: string): string[] {
  const found: string[] = []
  postcss.parse(css).walkDecls((decl) => {
    if (LAYOUT_PROPERTY.test(decl.prop)) found.push(`${(decl.parent as { selector?: string }).selector} { ${decl.prop} }`)
  })
  return found
}

describe('pixel console shell', () => {
  const dock = scopedStyles(dockSource)
  const topBar = scopedStyles(topBarSource)

  // Apart from the icon tag (P2), the only markup change is the TopBar logo tile (P3, pixel
  // logo fallback without the tile background); the hashes lock everything else.
  const withUpstreamIcon = (source: string) => source.replace(/<PixelIcon\b/g, '<Icon')

  it('keeps the Dock and TopBar markup unchanged apart from pixel icons and logo', () => {
    expect(templateHash(withUpstreamIcon(dockSource))).toBe('68589bed15dea4623a4459fde007fe4441d8ca55733628fd949aa8ae71dd6a3b')
    expect(templateHash(withUpstreamIcon(topBarSource))).toBe('761c0fef2ad5801b8e344a073ac9f73e0840d0a55a997d60474011c4b8d6c180')
  })

  it('draws the Dock as a solid pixel bar without blur', () => {
    const bar = declarationsIn(dock, '.portal-dock > ul')
    expect(bar['backdrop-filter']).toBe('none')
    expect(bar['border-width']).toBe('5px')
    expect(bar['border-color']).toBe('var(--pp-edge)')
    expect(bar['box-shadow']).toBe('none')
    expect(bar.outline).toBe('4px solid #f8f7f2')
    expect(bar['background-color']).toBe('#ffffff')
    expect(declarationsIn(dock, ':where(html.dark) .portal-dock > ul')['background-color']).toBe('#232630')
    expect(bar['border-image']).toBe('var(--pp-frame) 5 / 5px')
    expect(bar['background-clip']).toBe('border-box')
    expect(dockSource).toMatch(/@media \(forced-colors: active\)\s*\{\s*\.portal-dock > ul[^}]*border-image-source: none/)
  })

  it('marks the active Dock item with sage and charcoal instead of white on black', () => {
    const active = /const activeClass = '([^']+)'/.exec(dockSource)?.[1] ?? ''
    expect(active.split(' ')).toEqual(expect.arrayContaining(['bg-primary-500', 'text-gray-900', 'dark:bg-primary-400', 'dark:text-dark-900']))
    expect(active).not.toMatch(/text-white/)
    const { primary, gray, dark } = tailwindConfig.theme.extend.colors as Record<string, Record<number, string>>
    expect(contrastRatio(gray[900], primary[500])).toBeGreaterThanOrEqual(4.5)
    expect(contrastRatio(dark[900], primary[400])).toBeGreaterThanOrEqual(4.5)
  })

  it('sets Dock and TopBar titles in the pixel font on the 12px grid', () => {
    expect(declarationsIn(dock, '.portal-dock-label')['font-family']).toBe('var(--pp-font-pixel)')
    expect(fontSizes(dock, '.portal-dock-label')).toEqual(['12px'])
    expect(declarationsIn(topBar, 'h1')['font-family']).toBe('var(--pp-font-pixel)')
    expect(fontSizes(topBar, 'h1').every((size) => /^(12|24)px$/.test(size))).toBe(true)
  })

  it('touches no layout in the new scoped styles', () => {
    const dockLayout = layoutDeclarations(dock).filter((entry) => !entry.startsWith('.portal-dock {'))
    expect(dockLayout).toEqual([])
    expect(layoutDeclarations(topBar)).toEqual([])
  })

  it('keeps scoped styles from baking in round pills the theme cannot square', () => {
    const sources = import.meta.glob('../**/*.vue', { query: '?raw', import: 'default', eager: true }) as Record<string, string>
    const round = Object.entries(sources)
      .filter(([, source]) => /@apply[^;]*\brounded-full\b/.test(scopedStyles(source)))
      .map(([path]) => path)
    expect(round).toEqual([])
  })

  it('turns every shadow utility into no shadow', () => {
    const shadows = tailwindConfig.theme.extend.boxShadow as Record<string, string>
    for (const size of ['sm', 'DEFAULT', 'md', 'lg', 'xl', '2xl', 'card', 'card-hover', 'glow']) {
      expect(shadows[size], size).toBe('0 0 #0000')
    }
  })
})
