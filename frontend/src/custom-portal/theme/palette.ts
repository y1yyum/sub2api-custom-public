import { DEFAULT_PALETTE, PALETTE_ATTRIBUTE, findPalette, type PaletteMode, type PortalPalette } from './palettes'

/** Current palette, plus the last one used in each mode so a light/dark toggle restores it. */
export const PALETTE_STORAGE_KEY = 'portal-palette'
const modeKey = (mode: PaletteMode) => `${PALETTE_STORAGE_KEY}-${mode}`
/** Upstream `initThemeClass` reads this before the app mounts, so it must follow the palette's mode. */
const UPSTREAM_THEME_KEY = 'theme'

function modeOf(root: HTMLElement): PaletteMode {
  return root.classList.contains('dark') ? 'dark' : 'light'
}

export function rememberedPalette(storage: Storage, mode: PaletteMode): PortalPalette {
  const saved = findPalette(storage.getItem(modeKey(mode)))
  return saved?.mode === mode ? saved : DEFAULT_PALETTE[mode]
}

export function activePalette(doc: Document): PortalPalette {
  const root = doc.documentElement
  return findPalette(root.getAttribute(PALETTE_ATTRIBUTE)) ?? DEFAULT_PALETTE[modeOf(root)]
}

/** Applies a palette and its mode, persists both, and tells upstream toggles (`theme-change`). */
export function setPalette(doc: Document, storage: Storage, palette: PortalPalette): void {
  const root = doc.documentElement
  root.setAttribute(PALETTE_ATTRIBUTE, palette.id)
  root.classList.toggle('dark', palette.mode === 'dark')
  storage.setItem(PALETTE_STORAGE_KEY, palette.id)
  storage.setItem(modeKey(palette.mode), palette.id)
  storage.setItem(UPSTREAM_THEME_KEY, palette.mode)
  doc.defaultView?.dispatchEvent(new Event('theme-change'))
}

export function setPaletteMode(doc: Document, storage: Storage, mode: PaletteMode): void {
  setPalette(doc, storage, rememberedPalette(storage, mode))
}

/**
 * Picks the stored palette for the mode upstream already applied, then follows
 * `.dark` toggles made by upstream controls (ThemeToggle, admin sidebar) by
 * switching to the last palette used in the new mode.
 */
export function installPaletteSync(doc: Document, storage: Storage): () => void {
  const root = doc.documentElement
  const sync = () => {
    const mode = modeOf(root)
    if (findPalette(root.getAttribute(PALETTE_ATTRIBUTE))?.mode === mode) return
    const saved = findPalette(storage.getItem(PALETTE_STORAGE_KEY))
    const next = saved?.mode === mode ? saved : rememberedPalette(storage, mode)
    root.setAttribute(PALETTE_ATTRIBUTE, next.id)
    storage.setItem(PALETTE_STORAGE_KEY, next.id)
  }
  sync()
  const observer = new MutationObserver(sync)
  observer.observe(root, { attributes: true, attributeFilter: ['class'] })
  return () => observer.disconnect()
}
