// @vitest-environment node
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import avatarSource from '../shell/PortalAvatarMenu.vue?raw'
import dockSource from '../shell/PortalDock.vue?raw'
import topBarSource from '../shell/PortalTopBar.vue?raw'
import cardsSource from '../components/overview/OverviewMetricCards.vue?raw'
import ranksSource from '../components/overview/OverviewTopModels.vue?raw'
import overviewSource from '../views/PortalOverviewView.vue?raw'
import { METRIC_TONES } from '../overview/metricTones'
import { pixelFrame } from '../theme/pixelFrame'
import { contrastRatio, declarationsIn, scopedStyles } from './contrast'

const themeCss = readFileSync(resolve(__dirname, '../theme/rhine-theme.css'), 'utf8')
const light = declarationsIn(themeCss, ':where(html.theme-rhine)')
const dark = declarationsIn(themeCss, ':where(html.theme-rhine).dark')
const DOUBLE_RULE = '3px double currentColor'

/** Base rules only: declarationsIn also merges the matching rules inside @media blocks. */
const withoutMedia = (css: string) => css.replace(/@media[^{]*\{(?:[^{}]*\{[^}]*\})*\s*\}/g, '')

describe('pixel console overview', () => {
  it('builds tinted frames from the same nine-slice as the theme frame', () => {
    expect(pixelFrame('#2c2e33')).toBe(light['--pp-frame'])
    expect(pixelFrame('#383c4a')).toBe(dark['--pp-frame'])
  })

  it.each(Object.entries(METRIC_TONES))('frames the %s card in a rule colour that reads on its tint', (_, tone) => {
    for (const mode of ['light', 'dark'] as const) {
      const { tint, rule } = tone.surface[mode]
      expect(contrastRatio(rule, tint), mode).toBeGreaterThanOrEqual(3)
    }
  })

  it('draws metric cards in their tone, solid when selected, with pixel numbers', () => {
    const css = scopedStyles(cardsSource)
    const base = withoutMedia(css)
    expect(declarationsIn(base, '.metric-card.card')).toMatchObject({
      'background-color': 'var(--metric-tint)',
      'border-color': 'var(--metric-rule)',
      'border-image-source': 'var(--metric-frame)'
    })
    expect(declarationsIn(base, ':where(html.dark) .metric-card.card')['border-image-source']).toBe('var(--metric-frame-dark)')
    expect(declarationsIn(base, ".metric-card.card[aria-selected='true']")['border-image-source']).toBe('none')
    // Vue compiles `:global(x) .y` to a bare `x`, which would restyle the page root.
    expect(css).not.toMatch(/:global\(/)
    expect(declarationsIn(css, '.metric-icon').border).toBe(DOUBLE_RULE)
    expect(declarationsIn(base, '.metric-value')).toMatchObject({ 'font-family': 'var(--pp-font-pixel)', 'font-size': '24px' })
    expect(css).toMatch(/@media \(min-width: 640px\) \{\s*\.metric-value \{\s*font-size: 36px;/)
    expect(cardsSource).toMatch(/<PixelIcon :name="card\.tone\.icon" size="md" \/>/)
    expect(cardsSource).not.toMatch(/border-primary-600|rounded-full/)
  })

  it('shows the pixel cat in a double-ruled lilac square when there is no photo', () => {
    expect(avatarSource).not.toMatch(/initials/)
    expect(avatarSource).toMatch(/<span\s+v-else\s+class="avatar-cat [^"]*bg-violet-50[^"]*"[\s\S]*?<PixelIcon name="cat" size="md" \/>/)
    expect(declarationsIn(scopedStyles(avatarSource), '.avatar-cat').border).toBe(DOUBLE_RULE)
  })

  it('rules the TopBar bottom with the frame and double-rules the balance', () => {
    const css = scopedStyles(topBarSource)
    expect(declarationsIn(css, 'header')).toMatchObject({ 'border-bottom-width': '5px', 'border-image': 'var(--pp-frame) 5 / 0 0 5px 0' })
    expect(declarationsIn(css, "[data-testid='portal-balance']").border).toBe(DOUBLE_RULE)
  })

  it('sets the page title in the pixel font over the square-spiral band', () => {
    expect(light['--pp-huiwen']).toMatch(/^url\("data:image\/svg\+xml,<svg [^>]*width='8' height='8'.*fill='%232c2e33'/)
    expect(dark['--pp-huiwen']).toBe(light['--pp-huiwen'].replace('%232c2e33', '%23e6e8ee'))
    const title = declarationsIn(withoutMedia(scopedStyles(overviewSource)), '.overview-title')
    expect(title).toMatchObject({ 'font-family': 'var(--pp-font-pixel)', 'font-size': '24px' })
    expect(title.background).toBe('var(--pp-huiwen) left bottom / 8px 8px repeat-x')
  })

  it('double-rules the ranking bars and gives the leader a butter number', () => {
    const css = scopedStyles(ranksSource)
    expect(declarationsIn(css, '.rank-track').border).toBe(DOUBLE_RULE)
    expect(declarationsIn(css, '.rank-fill')['background-size']).toBe('2px 2px')
    expect(declarationsIn(css, '.rank-first')['background-color']).toBe('var(--pp-tone-butter-mid)')
  })

  it('marks the active Dock item with a 2px inset rule, not a shadow', () => {
    expect(/const activeClass = '([^']+)'/.exec(dockSource)?.[1].split(' ')).toContain('portal-dock-active')
    expect(declarationsIn(scopedStyles(dockSource), '.portal-dock-active')).toEqual({ outline: '2px solid currentColor', 'outline-offset': '-2px' })
  })
})
