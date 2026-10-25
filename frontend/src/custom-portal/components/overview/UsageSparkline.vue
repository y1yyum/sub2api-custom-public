<template>
  <svg
    class="block h-full w-full overflow-visible"
    :viewBox="`0 0 ${width} ${height}`"
    preserveAspectRatio="none"
    aria-hidden="true"
    focusable="false"
    data-testid="usage-sparkline"
  >
    <line
      v-if="!geometry"
      x1="0"
      :y1="height - PADDING"
      :x2="width"
      :y2="height - PADDING"
      class="text-gray-200 dark:text-dark-600"
      stroke="currentColor"
      stroke-width="1"
      vector-effect="non-scaling-stroke"
      data-testid="usage-sparkline-baseline"
    />
    <template v-else>
      <path
        v-if="geometry.path"
        :d="geometry.path"
        fill="none"
        stroke="currentColor"
        :stroke-width="strokeWidth"
        stroke-linecap="round"
        stroke-linejoin="round"
        vector-effect="non-scaling-stroke"
        data-testid="usage-sparkline-line"
      />
      <!-- A zero-length round-capped stroke stays circular under preserveAspectRatio="none". -->
      <path
        v-if="geometry.end"
        :d="`M${geometry.end.x} ${geometry.end.y}h0`"
        fill="none"
        stroke="currentColor"
        :stroke-width="strokeWidth * 3"
        stroke-linecap="round"
        vector-effect="non-scaling-stroke"
        data-testid="usage-sparkline-end"
      />
    </template>
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const PADDING = 2

const props = withDefaults(
  defineProps<{
    values: readonly (number | null)[]
    width?: number
    height?: number
    strokeWidth?: number
  }>(),
  { width: 100, height: 28, strokeWidth: 1.5 }
)

interface Point {
  x: number
  y: number
}

const round = (value: number) => Math.round(value * 100) / 100

const geometry = computed(() => {
  const known = props.values.filter((value): value is number => value !== null && Number.isFinite(value))
  if (!known.length) return null
  const min = Math.min(...known)
  const max = Math.max(...known)
  const span = max - min
  const usable = props.height - PADDING * 2
  const step = props.values.length > 1 ? props.width / (props.values.length - 1) : 0
  const toPoint = (value: number, index: number): Point => ({
    x: round(props.values.length > 1 ? index * step : props.width),
    y: round(span > 0 ? PADDING + usable * (1 - (value - min) / span) : PADDING + usable / 2)
  })

  let path = ''
  let penDown = false
  let end: Point | null = null
  props.values.forEach((value, index) => {
    if (value === null || !Number.isFinite(value)) {
      penDown = false
      return
    }
    const point = toPoint(value, index)
    path += `${penDown ? 'L' : 'M'}${point.x} ${point.y}`
    penDown = true
    end = point
  })
  return { path, end: end as Point | null }
})
</script>
