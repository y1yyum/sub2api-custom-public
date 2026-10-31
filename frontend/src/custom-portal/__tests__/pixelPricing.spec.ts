// @vitest-environment node
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import postcss, { type Rule } from 'postcss'
import pricingSource from '../components/rhine/RhineModelPricing.vue?raw'
import { pixelIconDataUri } from '../theme/pixelIcons'
import { declarationsIn, scopedStyles } from './contrast'

const themeCss = readFileSync(resolve(__dirname, '../theme/rhine-theme.css'), 'utf8')
const lightTones = declarationsIn(themeCss, ':where(html.theme-rhine)')
const css = scopedStyles(pricingSource)
const tokens = declarationsIn(css, '.model-status')

/** The landing is light only, so icons take the light tones and the landing ink. */
const lightColor = (fill: string) =>
  fill === 'currentColor' ? '#2c2e33' : lightTones[`--pp-tone-${/var\(--pp-tone-([\w-]+)\)/.exec(fill)![1]}`]

describe('landing pricing placeholders', () => {
  it.each(['cube', 'shield', 'inbox'] as const)('embeds the %s pixel icon in the light tones', (name) => {
    expect(tokens[`--rp-${name}`]).toBe(`url("${pixelIconDataUri(name, lightColor)}")`)
  })

  it('picks the icon by state over a square-dot field', () => {
    expect(declarationsIn(css, '.status-empty')).toMatchObject({
      '--rp-icon': 'var(--rp-inbox)',
      background: 'var(--rp-dots) 0 0 / 6px 6px'
    })
    expect(declarationsIn(css, "[data-state='loading'] .status-empty")['--rp-icon']).toBe('var(--rp-cube)')
    expect(declarationsIn(css, "[data-state='unavailable'] .status-empty")['--rp-icon']).toBe('var(--rp-shield)')
    const icon = declarationsIn(css, '.status-empty::before')
    expect([icon.width, icon.height, icon['image-rendering']]).toEqual(['48px', '48px', 'pixelated'])
  })

  it('shows a stepped stripe bar only while loading, still under reduced motion', () => {
    const rules: string[] = []
    postcss.parse(css).walkDecls('animation', (decl) => {
      if (decl.value.startsWith('rp-stripes')) rules.push((decl.parent as Rule).selector)
    })
    expect(rules).toEqual(["[data-state='loading'] .status-empty::after"])
    expect(declarationsIn(css, "[data-state='loading'] .status-empty::after").border).toBe('3px double var(--rh-ink, #2c2e33)')
    expect(css).toMatch(/animation: rp-stripes 400ms steps\(2\) infinite/)
    expect(css).toMatch(/@keyframes rp-stripes \{\s*to \{\s*background-position: 8px 0;/)
    expect(css).toMatch(/@media \(prefers-reduced-motion: reduce\) \{\s*\[data-state='loading'\] \.status-empty::after \{\s*animation: none;/)
  })
})
