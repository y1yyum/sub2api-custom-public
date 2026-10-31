import type { OverviewMetric } from './usageChartData'

/**
 * Classes are upstream Tailwind names that the console theme recolours. Chart
 * hexes bypass Tailwind, so they repeat the theme's muted 500 steps.
 */
export interface MetricTone {
  text: string
  bg: string
  /** Selected-card border/ring. */
  ring: string
  /** Line or bar colour for Chart.js and SVG strokes. */
  chart: string
  /** Translucent fill under the line (hex with alpha). */
  chartFill: string
  icon: MetricIcon
  /** Card tint and frame rule colour, per colour mode (hexes, drawn by scoped CSS). */
  surface: Readonly<Record<'light' | 'dark', MetricSurface>>
}

export interface MetricSurface {
  tint: string
  rule: string
}

/** Names from the pixel icon set. */
export type MetricIcon = 'cube' | 'swap' | 'dollar' | 'database'

export type TokenSeries = 'input' | 'output' | 'cache_creation' | 'cache_read'

export const METRIC_TONES: Readonly<Record<OverviewMetric, MetricTone>> = {
  tokens: {
    text: 'text-primary-600 dark:text-primary-400',
    bg: 'bg-primary-50 dark:bg-primary-900/20',
    ring: 'ring-primary-500/40 border-primary-500',
    chart: '#8fa888',
    chartFill: '#8fa88820',
    icon: 'cube',
    surface: { light: { tint: '#f1f5ef', rule: '#58724f' }, dark: { tint: '#233827', rule: '#8fa888' } }
  },
  requests: {
    text: 'text-blue-600 dark:text-blue-400',
    bg: 'bg-blue-50 dark:bg-blue-900/20',
    ring: 'ring-blue-500/40 border-blue-500',
    chart: '#6a8fb0',
    chartFill: '#6a8fb020',
    icon: 'swap',
    surface: { light: { tint: '#ebf1f6', rule: '#1e364a' }, dark: { tint: '#223342', rule: '#6a8fb0' } }
  },
  cost: {
    text: 'text-amber-600 dark:text-amber-400',
    bg: 'bg-amber-50 dark:bg-amber-900/20',
    ring: 'ring-amber-500/40 border-amber-500',
    chart: '#b8945a',
    chartFill: '#b8945a20',
    icon: 'dollar',
    surface: { light: { tint: '#f6efe0', rule: '#5a4214' }, dark: { tint: '#363020', rule: '#b8945a' } }
  },
  cacheHitRate: {
    text: 'text-violet-600 dark:text-violet-400',
    bg: 'bg-violet-50 dark:bg-violet-900/20',
    ring: 'ring-violet-500/40 border-violet-500',
    chart: '#9580b8',
    chartFill: '#9580b820',
    icon: 'database',
    surface: { light: { tint: '#f0ecf6', rule: '#3b2a52' }, dark: { tint: '#2f243d', rule: '#9580b8' } }
  }
}

/** Same series colours as upstream `TokenUsageTrend`. */
export const TOKEN_SERIES_COLORS: Readonly<Record<TokenSeries, string>> = {
  input: '#3b82f6',
  output: '#10b981',
  cache_creation: '#f59e0b',
  cache_read: '#06b6d4'
}

export const TOKEN_SERIES: readonly TokenSeries[] = ['input', 'output', 'cache_creation', 'cache_read']

/** Previous-period dashed line and mean reference line. */
export const COMPARISON_LINE_COLOR = '#9ca3af'

/** Heatmap levels 0 (no usage) to 4 in sage; dark mode climbs towards light. */
export const HEATMAP_LEVEL_CLASSES: readonly string[] = [
  'bg-gray-100 dark:bg-dark-700',
  'bg-primary-300 dark:bg-primary-600',
  'bg-primary-500 dark:bg-primary-500',
  'bg-primary-600 dark:bg-primary-300',
  'bg-primary-900 dark:bg-primary-100'
]

export function metricTone(metric: OverviewMetric): MetricTone {
  return METRIC_TONES[metric]
}
