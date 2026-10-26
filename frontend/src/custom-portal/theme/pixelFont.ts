import pixelFontUrl from './fonts/fusion-pixel-12px-proportional-zh_hans.subset.woff2?url'

/** Starts the font download before the first pixel-font glyph is laid out; idempotent. */
export function preloadPixelFont(doc: Document): void {
  if (doc.head.querySelector(`link[rel="preload"][href="${pixelFontUrl}"]`)) return
  const link = doc.createElement('link')
  link.rel = 'preload'
  link.setAttribute('as', 'font')
  link.type = 'font/woff2'
  link.setAttribute('href', pixelFontUrl)
  link.crossOrigin = 'anonymous'
  doc.head.append(link)
}
