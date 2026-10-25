export type HeatmapLevel = 0 | 1 | 2 | 3 | 4

export const HEATMAP_LEVEL_COUNT = 5

function isActive(value: number): boolean {
  return Number.isFinite(value) && value > 0
}

/** Index of the first element greater than `value` in an ascending list. */
function upperBound(sorted: readonly number[], value: number): number {
  let low = 0
  let high = sorted.length
  while (low < high) {
    const mid = (low + high) >>> 1
    if (sorted[mid] <= value) low = mid + 1
    else high = mid
  }
  return low
}

/**
 * Quartile scale over the non-zero values: a value's level is the quarter its
 * percentile rank (share of active values at or below it) falls into. Zero,
 * negative and non-finite values are level 0; the busiest day is always 4, so
 * a single active day or a run of equal days reads as the maximum.
 */
export function createHeatmapScale(values: readonly number[]): (value: number) => HeatmapLevel {
  const sorted = values.filter(isActive).sort((a, b) => a - b)
  return (value: number) => {
    if (!isActive(value) || sorted.length === 0) return 0
    const rank = upperBound(sorted, value) / sorted.length
    return Math.min(4, Math.max(1, Math.ceil(rank * 4))) as HeatmapLevel
  }
}

export function heatmapLevels(values: readonly number[]): HeatmapLevel[] {
  const scale = createHeatmapScale(values)
  return values.map(scale)
}
