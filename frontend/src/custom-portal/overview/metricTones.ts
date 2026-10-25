import type { OverviewMetric } from './usageChartData'

/**
 * Upstream Tailwind values only: the Rhine theme remaps primary/teal and the
 * neutrals, so nothing here is a portal-specific palette.
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
}

/** Names from the upstream `Icon` set. */
export type MetricIcon = 'cube' | 'swap' | 'dollar' | 'database'

export type TokenSeries = 'input' | 'output' | 'cache_creation' | 'cache_read'

export const METRIC_TONES: Readonly<Record<OverviewMetric, MetricTone>> = {
  tokens: {
    text: 'text-primary-600 dark:text-primary-400',
    bg: 'bg-primary-50 dark:bg-primary-900/20',
    ring: 'ring-primary-500/40 border-primary-500',
    chart: '#14b8a6',
    chartFill: '#14b8a620',
    icon: 'cube'
  },
  requests: {
    text: 'text-blue-600 dark:text-blue-400',
    bg: 'bg-blue-50 dark:bg-blue-900/20',
    ring: 'ring-blue-500/40 border-blue-500',
    chart: '#3b82f6',
    chartFill: '#3b82f620',
    icon: 'swap'
  },
  cost: {
    text: 'text-amber-600 dark:text-amber-400',
    bg: 'bg-amber-50 dark:bg-amber-900/20',
    ring: 'ring-amber-500/40 border-amber-500',
    chart: '#f59e0b',
    chartFill: '#f59e0b20',
    icon: 'dollar'
  },
  cacheHitRate: {
    text: 'text-violet-600 dark:text-violet-400',
    bg: 'bg-violet-50 dark:bg-violet-900/20',
    ring: 'ring-violet-500/40 border-violet-500',
    chart: '#8b5cf6',
    chartFill: '#8b5cf620',
    icon: 'database'
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

/** Heatmap levels 0 (no usage) to 4, the contribution-graph greens. */
export const HEATMAP_LEVEL_CLASSES: readonly string[] = [
  'bg-gray-100 dark:bg-dark-800',
  'bg-emerald-200 dark:bg-emerald-900',
  'bg-emerald-400 dark:bg-emerald-700',
  'bg-emerald-600 dark:bg-emerald-500',
  'bg-emerald-800 dark:bg-emerald-300'
]

export function metricTone(metric: OverviewMetric): MetricTone {
  return METRIC_TONES[metric]
}
