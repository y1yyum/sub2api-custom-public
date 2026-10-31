<template>
  <svg
    v-if="runs"
    :class="sizeClass"
    viewBox="0 0 16 16"
    shape-rendering="crispEdges"
    aria-hidden="true"
    data-pixel-icon
  >
    <rect v-for="run in runs" :key="`${run.x},${run.y}`" :x="run.x" :y="run.y" :width="run.width" height="1" :fill="run.fill" />
  </svg>
  <Icon v-else :name="name as UpstreamIconName" :size="size" aria-hidden="true" />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { isPixelIconName, pixelRuns, warnMissingPixelIcon, type PixelIconName } from '../../theme/pixelIcons'

type UpstreamIconName = InstanceType<typeof Icon>['$props']['name']

const props = withDefaults(
  defineProps<{
    name: PixelIconName | UpstreamIconName
    size?: 'xs' | 'sm' | 'md' | 'lg'
  }>(),
  { size: 'md' }
)

const runs = computed(() => {
  if (isPixelIconName(props.name)) return pixelRuns(props.name)
  if (import.meta.env.DEV) warnMissingPixelIcon(props.name)
  return null
})

// Whole multiples of the 16px art; upstream xs (12px) and md (20px) would blur it.
const sizeClass = computed(() => ({ xs: 'h-4 w-4', sm: 'h-4 w-4', md: 'h-8 w-8', lg: 'h-12 w-12' })[props.size])
</script>
