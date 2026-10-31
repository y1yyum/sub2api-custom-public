// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { PIXEL_LOGO_COLORS, PIXEL_LOGO_DATA_URI, PIXEL_LOGO_ROWS, PIXEL_LOGO_SVG } from '../theme/pixelLogo'

describe('pixel logo', () => {
  it('is a 32×32 grid of tile, ribbon and fold pixels', () => {
    expect(PIXEL_LOGO_ROWS).toHaveLength(32)
    for (const row of PIXEL_LOGO_ROWS) expect(row).toMatch(/^[.kom]{32}$/)
    expect(PIXEL_LOGO_COLORS).toEqual({ k: '#222423', o: '#FA643F', m: '#B83B1E' })
  })

  it('cuts the tile corners in 5/3/2/1/1 steps', () => {
    const inset = (row: string) => row.indexOf('k')
    expect(PIXEL_LOGO_ROWS.slice(0, 5).map(inset)).toEqual([5, 3, 2, 1, 1])
    expect(PIXEL_LOGO_ROWS.slice(-5).map(inset)).toEqual([1, 1, 2, 3, 5])
  })

  it('encodes every pixel exactly once in one path per colour', () => {
    const paths = [...PIXEL_LOGO_SVG.matchAll(/<path fill='(#\w+)' d='([^']+)'\/>/g)]
    expect(paths.map(([, fill]) => fill)).toEqual(Object.values(PIXEL_LOGO_COLORS))
    for (const [index, [, , d]] of paths.entries()) {
      const pixel = Object.keys(PIXEL_LOGO_COLORS)[index]
      const area = [...d.matchAll(/M\d+ \d+h(\d+)v1h-\1z/g)].reduce((sum, [, width]) => sum + Number(width), 0)
      expect(area, pixel).toBe(PIXEL_LOGO_ROWS.join('').split(pixel).length - 1)
    }
  })

  it('is a data URI usable as an img src and inside CSS url("…")', () => {
    expect(PIXEL_LOGO_DATA_URI.startsWith("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='32' height='32'")).toBe(true)
    expect(PIXEL_LOGO_DATA_URI).not.toMatch(/["#]/)
    expect(decodeURIComponent(PIXEL_LOGO_DATA_URI.slice('data:image/svg+xml,'.length))).toBe(PIXEL_LOGO_SVG)
  })
})
