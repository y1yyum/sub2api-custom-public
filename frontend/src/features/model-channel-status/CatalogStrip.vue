<template>
  <span class="flex h-5 items-end gap-px" role="group" :aria-label="label" data-testid="catalog-strip">
    <span
      v-for="(bucket, index) in buckets"
      :key="index"
      role="img"
      class="catalog-strip-block min-w-px flex-1 rounded-[1px]"
      :class="[healthBlockClass(bucket.health), cacheHeightClass(bucket.cache_level)]"
      :title="blockLabel(bucket, index)"
      :aria-label="blockLabel(bucket, index)"
      :data-cache-level="bucket.cache_level"
      :data-health="bucket.health"
    />
  </span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CatalogBucket, CatalogStripConfig } from '@/api/channelStatusCatalog'
import { bucketLabel, cacheHeightClass, healthBlockClass } from './catalogFormat'

/** Availability strip: colour is the success-rate step, height the cache-rate step. */
const props = defineProps<{
  buckets: CatalogBucket[]
  strip?: CatalogStripConfig | null
  label: string
}>()

const { t, locale } = useI18n()

function blockLabel(bucket: CatalogBucket, index: number): string {
  return bucketLabel(bucket, t, locale.value, { index, count: props.buckets.length, strip: props.strip })
}
</script>
<style scoped>
:where(html.theme-rhine) .catalog-strip-block[data-health='healthy'] { background-color: var(--pp-tone-mint-mid); }
:where(html.theme-rhine) .catalog-strip-block[data-health='warning'] { background-color: var(--pp-tone-butter-mid); }
:where(html.theme-rhine) .catalog-strip-block[data-health='critical'] { background-color: var(--pp-tone-peach-mid); }
</style>
