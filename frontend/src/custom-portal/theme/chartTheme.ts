import type { Chart, ChartConfiguration, Plugin, Tooltip } from 'chart.js'
import { THEME_ROOT_CLASS } from './install'

type ChartStatic = typeof Chart
type ChartModule = { Chart: ChartStatic; Tooltip: typeof Tooltip }
type ColorTable = Readonly<Record<string, string>>

/**
 * Upstream charts hard-code Tailwind neutrals; these map them step for step
 * onto the console grays. Upstream `dark-*` is Tailwind slate.
 */
const NEUTRALS: ColorTable = {
  // gray
  f9fafb: 'f8f7f2',
  f3f4f6: 'efede6',
  e5e7eb: 'e3e1d9',
  d1d5db: 'cfccc2',
  '9ca3af': 'a3a69c',
  '6b7280': '686c62',
  '4b5563': '55594f',
  '374151': '43463f',
  '1f2937': '34363a',
  '111827': '2c2e33',
  '030712': '1f2024',
  // slate
  f8fafc: 'f3f4f7',
  f1f5f9: 'e6e8ee',
  e2e8f0: 'd3d6df',
  cbd5e1: 'b8bdcb',
  '94a3b8': '9aa0b2',
  '64748b': '7d8396',
  '475569': '6b7186',
  '334155': '383c4a',
  '1e293b': '232630',
  '0f172a': '1c1e26',
  '020617': '16181f'
}

/**
 * Upstream primary (teal) onto sage. Applied only to the sole series of a
 * chart: across several datasets or inside a palette array teal is a
 * categorical colour.
 */
const BRAND: ColorTable = {
  f0fdfa: 'f1f5ef',
  ccfbf1: 'e2ebdf',
  '99f6e4': 'c9d8c4',
  '5eead4': 'b4c8ae',
  '2dd4bf': 'a3bca0',
  '14b8a6': '8fa888',
  '0d9488': '58724f',
  '0f766e': '46603f',
  '115e59': '364a31',
  '134e4a': '283826',
  '042f2e': '192317'
}

const SERIES: ColorTable = { ...NEUTRALS, ...BRAND }

const HEX = /^#([0-9a-f]{6})([0-9a-f]{2})?$/i
const RGB = /^rgba?\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*(?:,\s*([\d.]+%?)\s*)?\)$/i
const COLOR_KEY = /colou?r$/i

function toHexPair(channel: string): string {
  return Number(channel).toString(16).padStart(2, '0')
}

/** Maps one colour string through `table`, preserving its alpha and notation. */
export function mapColor(value: unknown, table: ColorTable = SERIES): unknown {
  if (typeof value !== 'string') return value
  const text = value.trim()
  const hex = HEX.exec(text)
  if (hex) {
    const mapped = table[hex[1].toLowerCase()]
    return mapped ? `#${mapped}${hex[2] ?? ''}` : value
  }
  const rgb = RGB.exec(text)
  if (!rgb) return value
  const mapped = table[toHexPair(rgb[1]) + toHexPair(rgb[2]) + toHexPair(rgb[3])]
  if (!mapped) return value
  const [r, g, b] = [0, 2, 4].map((offset) => parseInt(mapped.slice(offset, offset + 2), 16))
  return rgb[4] === undefined ? `rgb(${r}, ${g}, ${b})` : `rgba(${r}, ${g}, ${b}, ${rgb[4]})`
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (value === null || typeof value !== 'object') return false
  const proto = Object.getPrototypeOf(value)
  return proto === Object.prototype || proto === null
}

function recolorOptions(node: unknown, seen: WeakSet<object>): void {
  if (Array.isArray(node)) {
    node.forEach((item) => recolorOptions(item, seen))
    return
  }
  if (!isPlainObject(node) || seen.has(node)) return
  seen.add(node)
  for (const [key, value] of Object.entries(node)) {
    if (COLOR_KEY.test(key)) {
      node[key] = Array.isArray(value) ? value.map((item) => mapColor(item, NEUTRALS)) : mapColor(value, NEUTRALS)
    } else {
      recolorOptions(value, seen)
    }
  }
}

function recolorDataset(dataset: Record<string, unknown>, scalarTable: ColorTable): void {
  for (const [key, value] of Object.entries(dataset)) {
    if (!COLOR_KEY.test(key)) continue
    dataset[key] = Array.isArray(value) ? value.map((item) => mapColor(item, NEUTRALS)) : mapColor(value, scalarTable)
  }
}

/** Pixel geometry: straight line segments, square points and square bars. Idempotent. */
function squareGeometry(config: Pick<ChartConfiguration, 'type' | 'data'>): void {
  for (const dataset of (config.data?.datasets ?? []) as unknown as Record<string, unknown>[]) {
    const type = dataset.type ?? config.type
    if (type === 'line') {
      dataset.tension = 0
      dataset.pointStyle = 'rect'
    }
    else if (type === 'bar') dataset.borderRadius = 0
  }
}

/** Rewrites upstream colours in place; mapping is idempotent. */
export function recolorChartConfig(config: Pick<ChartConfiguration, 'data' | 'options'>): void {
  recolorOptions(config.options, new WeakSet())
  const datasets = config.data?.datasets ?? []
  const scalarTable = datasets.length === 1 ? SERIES : NEUTRALS
  for (const dataset of datasets) {
    recolorDataset(dataset as unknown as Record<string, unknown>, scalarTable)
  }
}

function themeActive(chart: Chart): boolean {
  const doc = chart.canvas?.ownerDocument
  return !!doc && doc.documentElement.classList.contains(THEME_ROOT_CLASS)
}

export const rhineChartPlugin: Plugin = {
  id: 'rhineTheme',
  // Runs after scales are rebuilt but before any colour is resolved for layout or drawing.
  beforeUpdate(chart) {
    if (!themeActive(chart)) return
    const config = chart.config as unknown as ChartConfiguration
    recolorChartConfig(config)
    squareGeometry(config)
  }
}

function applyDefaults(ChartJS: ChartStatic, dark: boolean): void {
  const { defaults } = ChartJS
  defaults.color = dark ? '#9aa0b2' : '#55594f'
  defaults.borderColor = dark ? 'rgba(230, 232, 238, 0.08)' : 'rgba(44, 46, 51, 0.08)'
  // Exists only once the Tooltip plugin is registered (the loader does that).
  const tooltip = defaults.plugins.tooltip
  if (!tooltip) return
  tooltip.backgroundColor = dark ? '#232630' : '#ffffff'
  tooltip.titleColor = dark ? '#e6e8ee' : '#2c2e33'
  tooltip.bodyColor = dark ? '#d3d6df' : '#43463f'
  // Charcoal vanishes on the dark card, so dark mode uses the control border.
  tooltip.borderColor = dark ? '#6b7186' : '#2c2e33'
  tooltip.borderWidth = 2
  tooltip.cornerRadius = 0
}

function snapshotDefaults(ChartJS: ChartStatic): () => void {
  const { defaults } = ChartJS
  const tooltip = defaults.plugins.tooltip
  const saved = {
    color: defaults.color,
    borderColor: defaults.borderColor,
    tooltip: tooltip && {
      backgroundColor: tooltip.backgroundColor,
      titleColor: tooltip.titleColor,
      bodyColor: tooltip.bodyColor,
      borderColor: tooltip.borderColor,
      borderWidth: tooltip.borderWidth,
      cornerRadius: tooltip.cornerRadius
    }
  }
  return () => {
    defaults.color = saved.color
    defaults.borderColor = saved.borderColor
    if (tooltip && saved.tooltip) Object.assign(tooltip, saved.tooltip)
  }
}

function refreshCharts(ChartJS: ChartStatic): void {
  for (const chart of Object.values(ChartJS.instances)) chart.update('none')
}

/**
 * Registers the recolouring plugin and pixel-pastel defaults, follows dark-mode
 * toggles, and refreshes charts that were created before the theme loaded.
 */
export function installRhineChartTheme(ChartJS: ChartStatic, doc: Document): () => void {
  const root = doc.documentElement
  ChartJS.register(rhineChartPlugin)
  const restoreDefaults = snapshotDefaults(ChartJS)
  let dark = root.classList.contains('dark')
  applyDefaults(ChartJS, dark)
  refreshCharts(ChartJS)

  const observer = new MutationObserver(() => {
    const nextDark = root.classList.contains('dark')
    if (nextDark === dark) return
    dark = nextDark
    applyDefaults(ChartJS, dark)
    refreshCharts(ChartJS)
  })
  observer.observe(root, { attributes: true, attributeFilter: ['class'] })

  return () => {
    observer.disconnect()
    ChartJS.unregister(rhineChartPlugin)
    restoreDefaults()
    refreshCharts(ChartJS)
  }
}

let loading: Promise<void> | undefined

/**
 * Loads chart.js on demand (it shares upstream's `vendor-chart` chunk, so the
 * same module instance is themed) and installs the theme once. Tooltip is
 * registered first so its defaults exist; every upstream chart registers it
 * anyway and re-registering keeps existing defaults.
 */
export function loadRhineChartTheme(
  doc: Document,
  importChart: () => Promise<ChartModule> = () => import('chart.js')
): Promise<void> {
  loading ??= importChart()
    .then(({ Chart: ChartJS, Tooltip: TooltipPlugin }) => {
      ChartJS.register(TooltipPlugin)
      installRhineChartTheme(ChartJS, doc)
    })
    .catch((error: unknown) => {
      loading = undefined
      console.warn('[custom-portal] Rhine chart theme failed to load; charts keep upstream colours.', error)
    })
  return loading
}

export function resetRhineChartThemeLoaderForTest(): void {
  loading = undefined
}
