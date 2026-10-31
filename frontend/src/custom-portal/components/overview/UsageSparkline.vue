<template>
  <svg
    class="block h-full w-full"
    :viewBox="`0 0 ${width} ${height}`"
    shape-rendering="crispEdges"
    aria-hidden="true"
    focusable="false"
    data-testid="usage-sparkline"
  >
    <rect
      v-if="!bars"
      x="0"
      :y="height - 2"
      :width="width"
      height="2"
      class="text-gray-200 dark:text-dark-600"
      fill="currentColor"
      data-testid="usage-sparkline-baseline"
    />
    <template v-else>
      <rect
        v-for="bar in bars"
        :key="bar.x"
        :x="bar.x"
        :y="height - bar.height"
        :width="BAR_WIDTH"
        :height="bar.height"
        fill="currentColor"
        :fill-opacity="bar.opacity"
        data-testid="usage-sparkline-bar"
      />
    </template>
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'

/** Ten square bars, 6px wide on an 8px pitch, heights in 4px steps. */
const BAR_COUNT = 10
const BAR_PITCH = 8
const BAR_WIDTH = 6
const STEP = 4

const props = withDefaults(
  defineProps<{
    values: readonly (number | null)[]
    width?: number
    height?: number
  }>(),
  { width: BAR_COUNT * BAR_PITCH, height: 32 }
)

const known = (value: number | null): value is number => value !== null && Number.isFinite(value)

/** Averages the series into at most ten contiguous buckets; a bucket without data is null. */
function buckets(values: readonly (number | null)[]): (number | null)[] {
  const count = Math.min(BAR_COUNT, values.length)
  return Array.from({ length: count }, (_, index) => {
    const slice = values
      .slice(Math.floor((index * values.length) / count), Math.floor(((index + 1) * values.length) / count))
      .filter(known)
    return slice.length ? slice.reduce((sum, value) => sum + Math.max(0, value), 0) / slice.length : null
  })
}

const bars = computed(() => {
  const averages = buckets(props.values)
  const max = Math.max(0, ...averages.filter(known))
  if (max <= 0) return null
  const steps = Math.floor(props.height / STEP)
  return averages.flatMap((value, index) => {
    if (value === null || value <= 0) return []
    return [
      {
        x: index * BAR_PITCH,
        height: Math.max(1, Math.round((value / max) * steps)) * STEP,
        opacity: Math.round((0.4 + (0.6 * (index + 1)) / averages.length) * 100) / 100
      }
    ]
  })
})
</script>
