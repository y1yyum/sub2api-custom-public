import type { Chart, ChartConfiguration, Plugin, Tooltip } from 'chart.js'
import { THEME_ROOT_CLASS } from './install'
import { activePalette } from './palette'
import { DEFAULT_PALETTE, PALETTE_ATTRIBUTE, SCALE_STEPS, type ColorScale, type PortalPalette } from './palettes'

type ChartStatic = typeof Chart
type ChartModule = { Chart: ChartStatic; Tooltip: typeof Tooltip }
type ColorTable = Readonly<Record<string, string>>

/** Upstream Tailwind steps 50–950: gray, slate (upstream `dark-*`) and the teal primary. */
const UPSTREAM_GRAY = ['f9fafb', 'f3f4f6', 'e5e7eb', 'd1d5db', '9ca3af', '6b7280', '4b5563', '374151', '1f2937', '111827', '030712']
const UPSTREAM_SLATE = ['f8fafc', 'f1f5f9', 'e2e8f0', 'cbd5e1', '94a3b8', '64748b', '475569', '334155', '1e293b', '0f172a', '020617']
const UPSTREAM_TEAL = ['f0fdfa', 'ccfbf1', '99f6e4', '5eead4', '2dd4bf', '14b8a6', '0d9488', '0f766e', '115e59', '134e4a', '042f2e']
/** Portal charts draw in the sage scale; other palettes swap it for their primary. */
const SAGE = SCALE_STEPS.map((step) => DEFAULT_PALETTE.light.primary[step].slice(1))

function stepTable(from: readonly string[], to: ColorScale): Record<string, string> {
  return Object.fromEntries(from.map((hex, i) => [hex, to[SCALE_STEPS[i]].slice(1)]))
}

interface ChartTables {
  /** Upstream neutrals onto the palette grays, step for step. */
  neutrals: ColorTable
  /**
   * Neutrals plus teal/sage onto the palette primary. Applied only to the sole
   * series of a chart: across several datasets or inside a palette array they
   * are categorical colours.
   */
  series: ColorTable
}

export function chartTables(palette: PortalPalette): ChartTables {
  const neutrals = { ...stepTable(UPSTREAM_GRAY, palette.gray), ...stepTable(UPSTREAM_SLATE, palette.dark) }
  return {
    neutrals,
    series: { ...neutrals, ...stepTable(SAGE, palette.primary), ...stepTable(UPSTREAM_TEAL, palette.primary) }
  }
}

let tables = chartTables(DEFAULT_PALETTE.light)

const HEX = /^#([0-9a-f]{6})([0-9a-f]{2})?$/i
const RGB = /^rgba?\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*(?:,\s*([\d.]+%?)\s*)?\)$/i
const COLOR_KEY = /colou?r$/i

function toHexPair(channel: string): string {
  return Number(channel).toString(16).padStart(2, '0')
}

/** Maps one colour string through `table`, preserving its alpha and notation. */
export function mapColor(value: unknown, table: ColorTable = tables.series): unknown {
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

interface Recolored {
  source: unknown
  written: unknown
}

/** Colours as the chart author set them, so a palette switch remaps from the original rather than the previous palette. */
const recolored = new WeakMap<object, Map<string, Recolored>>()

function sameValue(a: unknown, b: unknown): boolean {
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((item, i) => item === b[i])
  return a === b
}

function recolor(node: Record<string, unknown>, key: string, scalarTable: ColorTable): void {
  let entries = recolored.get(node)
  const previous = entries?.get(key)
  const source = previous && sameValue(previous.written, node[key]) ? previous.source : node[key]
  const written = Array.isArray(source)
    ? source.map((item) => mapColor(item, tables.neutrals))
    : mapColor(source, scalarTable)
  node[key] = written
  if (!entries) recolored.set(node, (entries = new Map()))
  entries.set(key, { source, written })
}

function recolorOptions(node: unknown, seen: WeakSet<object>): void {
  if (Array.isArray(node)) {
    node.forEach((item) => recolorOptions(item, seen))
    return
  }
  if (!isPlainObject(node) || seen.has(node)) return
  seen.add(node)
  for (const [key, value] of Object.entries(node)) {
    if (COLOR_KEY.test(key)) recolor(node, key, tables.neutrals)
    else recolorOptions(value, seen)
  }
}

function recolorDataset(dataset: Record<string, unknown>, scalarTable: ColorTable): void {
  for (const key of Object.keys(dataset)) {
    if (COLOR_KEY.test(key)) recolor(dataset, key, scalarTable)
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

/** Rewrites upstream colours in place from their original values; reruns are idempotent per palette. */
export function recolorChartConfig(config: Pick<ChartConfiguration, 'data' | 'options'>): void {
  recolorOptions(config.options, new WeakSet())
  const datasets = config.data?.datasets ?? []
  const scalarTable = datasets.length === 1 ? tables.series : tables.neutrals
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

function translucent(hex: string, alpha: number): string {
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16))
  return `rgba(${r}, ${g}, ${b}, ${alpha})`
}

function applyDefaults(ChartJS: ChartStatic, palette: PortalPalette): void {
  const { defaults } = ChartJS
  const dark = palette.mode === 'dark'
  const { ink, surface, control } = palette.tokens
  defaults.color = dark ? palette.dark[400] : palette.gray[600]
  defaults.borderColor = translucent(ink, 0.08)
  // Exists only once the Tooltip plugin is registered (the loader does that).
  const tooltip = defaults.plugins.tooltip
  if (!tooltip) return
  tooltip.backgroundColor = surface
  tooltip.titleColor = ink
  tooltip.bodyColor = dark ? palette.dark[200] : palette.gray[700]
  // Ink vanishes on the dark card, so dark mode uses the control border (equal to ink in light).
  tooltip.borderColor = control
  tooltip.borderWidth = 2
  tooltip.cornerRadius = 0
}

function usePalette(ChartJS: ChartStatic, palette: PortalPalette): void {
  tables = chartTables(palette)
  applyDefaults(ChartJS, palette)
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
 * Registers the recolouring plugin and the palette's defaults, follows palette
 * and dark-mode switches, and refreshes charts created before the theme loaded.
 */
export function installRhineChartTheme(ChartJS: ChartStatic, doc: Document): () => void {
  const root = doc.documentElement
  ChartJS.register(rhineChartPlugin)
  const restoreDefaults = snapshotDefaults(ChartJS)
  let palette = activePalette(doc)
  usePalette(ChartJS, palette)
  refreshCharts(ChartJS)

  const observer = new MutationObserver(() => {
    const next = activePalette(doc)
    if (next === palette) return
    palette = next
    usePalette(ChartJS, palette)
    refreshCharts(ChartJS)
  })
  observer.observe(root, { attributes: true, attributeFilter: ['class', PALETTE_ATTRIBUTE] })

  return () => {
    observer.disconnect()
    ChartJS.unregister(rhineChartPlugin)
    restoreDefaults()
    tables = chartTables(DEFAULT_PALETTE.light)
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
