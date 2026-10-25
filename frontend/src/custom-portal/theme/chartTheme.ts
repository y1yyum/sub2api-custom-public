import type { Chart, ChartConfiguration, Plugin, Tooltip } from 'chart.js'
import { THEME_ROOT_CLASS } from './install'

type ChartStatic = typeof Chart
type ChartModule = { Chart: ChartStatic; Tooltip: typeof Tooltip }
type ColorTable = Readonly<Record<string, string>>

/**
 * Upstream charts hard-code Tailwind neutrals; these map them onto the Rhine
 * warm grays. Upstream `dark-*` is Tailwind slate.
 */
const NEUTRALS: ColorTable = {
  // gray
  f9fafb: 'f1f0ea',
  f3f4f6: 'e8e6df',
  e5e7eb: 'd9d7cd',
  d1d5db: 'c1c3b3',
  '9ca3af': '989c8d',
  '6b7280': '6e7267',
  '4b5563': '5c6056',
  '374151': '44483f',
  '1f2937': '2c2f29',
  '111827': '191b18',
  '030712': '0f100e',
  // slate
  f8fafc: 'eef0ea',
  f1f5f9: 'e0e3dc',
  e2e8f0: 'cdd3cb',
  cbd5e1: 'b3bcb4',
  '94a3b8': '8a948c',
  '64748b': '6b766d',
  '475569': '414a43',
  '334155': '2b332f',
  '1e293b': '1c211f',
  '0f172a': '141816',
  '020617': '0d0f0e'
}

/**
 * Upstream primary (teal). Applied only to the sole series of a chart: across
 * several datasets or inside a palette array teal is a categorical colour.
 */
const BRAND: ColorTable = {
  f0fdfa: 'f6f8e4',
  ccfbf1: 'edf1c6',
  '99f6e4': 'dfe79b',
  '5eead4': 'd0dd78',
  '2dd4bf': 'c3d45a',
  '14b8a6': '7a8820',
  '0d9488': '5f6b16',
  '0f766e': '4c5612',
  '115e59': '3b430f',
  '134e4a': '2c320c',
  '042f2e': '1a1e06'
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
    if (themeActive(chart)) recolorChartConfig(chart.config as unknown as ChartConfiguration)
  }
}

function applyDefaults(ChartJS: ChartStatic, dark: boolean): void {
  const { defaults } = ChartJS
  defaults.color = dark ? '#b3bcb4' : '#5c6056'
  defaults.borderColor = dark ? 'rgba(224, 227, 220, 0.08)' : 'rgba(25, 27, 24, 0.08)'
  // Exists only once the Tooltip plugin is registered (the loader does that).
  const tooltip = defaults.plugins.tooltip
  if (!tooltip) return
  tooltip.backgroundColor = dark ? '#0d0f0e' : '#191b18'
  tooltip.titleColor = '#f9f9f2'
  tooltip.bodyColor = '#e8e6df'
  tooltip.borderColor = '#c3d45a'
  tooltip.borderWidth = 1
  tooltip.cornerRadius = 2
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
 * Registers the recolouring plugin and Rhine defaults, follows dark-mode
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
