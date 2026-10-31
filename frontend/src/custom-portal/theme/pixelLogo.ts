/**
 * The default logo redrawn on a 32×32 grid: `k` the tile, `o` the "2e"
 * ribbon and star, `m` the ribbon fold. Brand colours, not theme tones, so
 * the logo looks the same in light and dark mode. Show it only at 32px or 64px.
 */
export const PIXEL_LOGO_ROWS = [
  '.....kkkkkkkkkkkkkkkkkkkkkk.....',
  '...kkkkkkkkkkkkkkkkkkkkkkkkkk...',
  '..kkkkkkkkkkkkkkkkkkkkkkkkkkkk..',
  '.kkkkkkkkkkkkkkkkkkkkkkkkkkkkkk.',
  '.kkkkkkkkkkkkkkkkkkkkkkkkkkkkkk.',
  'kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk',
  'kkokkkkkkkkkkkkkkkkkkkkkkkkkkkkk',
  'kkkkkokkkkkkkkkkkkkkkkkkkkkkkkkk',
  'kkkkkokkkkkkkkkkkkkkkkkkkkkkkkkk',
  'kkkoooookkkkkkkkkkkkkkkkkkkkkkkk',
  'kkkkkokkkkkooookkkkkkkkkkkkkkkkk',
  'kkkkkokkkooooooookkkkkkkkkkkkkkk',
  'kkkkkkkkoookkkoookkkkkkkkkkkkkkk',
  'kkkkkkkkookkkkoookkkoooookkkkkkk',
  'kkkkkkkkkkkkkoookkkoooooookkkkkk',
  'kkkkkkkkkkkkoookkkoookkkoookkkkk',
  'kkkkkkkkkkkoookkkkoookkkoookkkkk',
  'kkkkkkkkkkoookkkkkoooooooookkkkk',
  'kkkkkkkkkoookkkkkkoookkkkkkkkkkk',
  'kkkkkkkkoookkkkkkkoookkkkkkkkkkk',
  'kkkkkkkkooooooooomoookkkkookkkkk',
  'kkkkkkkkooooooooommoooooookkkkkk',
  'kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk',
  'kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk',
  'kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk',
  'kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk',
  'kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk',
  '.kkkkkkkkkkkkkkkkkkkkkkkkkkkkkk.',
  '.kkkkkkkkkkkkkkkkkkkkkkkkkkkkkk.',
  '..kkkkkkkkkkkkkkkkkkkkkkkkkkkk..',
  '...kkkkkkkkkkkkkkkkkkkkkkkkkk...',
  '.....kkkkkkkkkkkkkkkkkkkkkk.....'
] as const

export const PIXEL_LOGO_COLORS = { k: '#222423', o: '#FA643F', m: '#B83B1E' } as const

type LogoPixel = keyof typeof PIXEL_LOGO_COLORS

/** One path per colour, built from horizontal runs. */
function logoSvg(): string {
  const paths = (Object.keys(PIXEL_LOGO_COLORS) as LogoPixel[]).map((pixel) => {
    let d = ''
    PIXEL_LOGO_ROWS.forEach((row, y) => {
      for (const match of row.matchAll(new RegExp(`${pixel}+`, 'g'))) d += `M${match.index} ${y}h${match[0].length}v1h-${match[0].length}z`
    })
    return `<path fill='${PIXEL_LOGO_COLORS[pixel]}' d='${d}'/>`
  })
  return `<svg xmlns='http://www.w3.org/2000/svg' width='32' height='32' viewBox='0 0 32 32' shape-rendering='crispEdges'>${paths.join('')}</svg>`
}

export const PIXEL_LOGO_SVG = logoSvg()

export const PIXEL_LOGO_DATA_URI = `data:image/svg+xml,${PIXEL_LOGO_SVG.replace(/#/g, '%23')}`
