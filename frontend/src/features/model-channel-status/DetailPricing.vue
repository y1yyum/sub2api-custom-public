<template>
  <section :aria-labelledby="headingId" data-testid="detail-pricing">
    <h3 :id="headingId" class="mb-2 text-sm font-semibold text-gray-900 dark:text-white">
      {{ t('modelChannelStatus.detail.pricing.title') }}
    </h3>
    <p v-if="block.error_code || !block.data" class="text-sm text-amber-700 dark:text-amber-300" data-testid="pricing-unavailable">
      {{ t('modelChannelStatus.detail.loadFailed') }}
    </p>
    <template v-else>
      <p class="mb-2 text-xs text-gray-500 dark:text-dark-400" data-testid="pricing-rate-note">{{ rateNote }}</p>

      <p v-if="!anyPrice" class="text-sm text-gray-500 dark:text-dark-400" data-testid="pricing-unset">
        {{ t('modelChannelStatus.detail.pricing.unset') }}
      </p>

      <template v-else>
        <p class="mb-1 text-xs font-medium text-gray-500 dark:text-dark-400">
          {{ t('modelChannelStatus.detail.pricing.paid') }} · {{ unitLabel }}
        </p>
        <dl class="m-0 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-sm" data-testid="pricing-paid">
          <template v-for="(line, index) in paidLines" :key="`${index}:${line.label}`">
            <dt class="text-gray-600 dark:text-dark-300">{{ line.label }}</dt>
            <dd class="m-0 text-right font-medium tabular-nums text-gray-900 dark:text-white">{{ line.value }}</dd>
          </template>
        </dl>

        <div v-if="pricing.request_tiers && pricing.request_tiers.length > 0" class="mt-3" data-testid="pricing-request-tiers">
          <p class="mb-1 text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.pricing.requestTiers') }}</p>
          <dl class="m-0 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-sm">
            <template v-for="(tier, index) in pricing.request_tiers" :key="`${index}:${tier.label}`">
              <dt class="text-gray-600 dark:text-dark-300">{{ tier.label }}</dt>
              <dd class="m-0 text-right font-medium tabular-nums text-gray-900 dark:text-white">{{ perUnit(tier.price) }}</dd>
            </template>
          </dl>
        </div>

        <div v-if="pricing.long_context && pricing.long_context.length > 0" class="mt-3" data-testid="pricing-context-tiers">
          <p class="mb-1 text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.pricing.contextTiers') }}</p>
          <p v-if="!pricing.long_context_enabled" class="mb-1 text-xs text-amber-700 dark:text-amber-300" data-testid="pricing-long-context-off">
            {{ t('modelChannelStatus.detail.pricing.longContextDisabled') }}
          </p>
          <table class="w-full text-left text-xs tabular-nums">
            <thead>
              <tr class="text-gray-500 dark:text-dark-400">
                <th scope="col" class="py-1 pr-2 font-medium">{{ t('modelChannelStatus.detail.pricing.contextTiers') }}</th>
                <th scope="col" class="py-1 pr-2 text-right font-medium">{{ t('modelChannelStatus.detail.pricing.input') }}</th>
                <th scope="col" class="py-1 text-right font-medium">{{ t('modelChannelStatus.detail.pricing.output') }}</th>
              </tr>
            </thead>
            <tbody class="text-gray-900 dark:text-white">
              <tr v-for="(tier, index) in pricing.long_context" :key="`${index}:${tier.min_tokens}`">
                <td class="py-0.5 pr-2">{{ tierRange(tier.min_tokens, tier.max_tokens) }}</td>
                <td class="py-0.5 pr-2 text-right">{{ perMillion(tier.paid.input) }}</td>
                <td class="py-0.5 text-right">{{ perMillion(tier.paid.output) }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <ul v-if="tags.length > 0" class="m-0 mt-3 flex list-none flex-wrap gap-1.5 p-0" data-testid="pricing-tags">
          <li
            v-for="(tag, index) in tags"
            :key="`${index}:${tag.text}`"
            class="rounded bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-600 dark:bg-dark-700 dark:text-dark-300"
            :title="tag.title"
          >
            {{ tag.text }}
          </li>
        </ul>

        <details v-if="officialLines.length > 0" class="mt-3" data-testid="pricing-official">
          <summary class="cursor-pointer text-xs text-primary-600 dark:text-primary-300">
            {{ t('modelChannelStatus.detail.pricing.official') }}
          </summary>
          <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.pricing.officialHint') }}</p>
          <dl class="m-0 mt-1 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-sm">
            <template v-for="line in officialLines" :key="line.label">
              <dt class="text-gray-600 dark:text-dark-300">{{ line.label }}</dt>
              <dd class="m-0 text-right tabular-nums text-gray-700 dark:text-dark-200">{{ line.value }}</dd>
            </template>
          </dl>
        </details>
      </template>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CatalogBlock, CatalogPaidPrices, CatalogPricing } from '@/api/channelStatusCatalog'
import { formatMultiplier, formatTokens, perMillion, perUnit } from './catalogFormat'

const props = defineProps<{ block: CatalogBlock<CatalogPricing> }>()

const { t } = useI18n()
const headingId = 'catalog-detail-pricing-heading'

interface Line {
  label: string
  value: string
}

// The template only renders these once `block.data` is present.
const pricing = computed(() => props.block.data as CatalogPricing)
const isToken = computed(() => pricing.value.billing_mode === 'token')

const unitLabel = computed(() => {
  if (isToken.value) return t('modelChannelStatus.detail.pricing.unitPerMillion')
  const unit =
    pricing.value.billing_mode === 'image'
      ? t('modelChannelStatus.detail.pricing.perImage')
      : pricing.value.billing_mode === 'video'
        ? t('modelChannelStatus.detail.pricing.perVideo')
        : t('modelChannelStatus.detail.pricing.perRequest')
  return t('modelChannelStatus.detail.pricing.unitPerUnit', { unit })
})

const rateNote = computed(() => {
  const { rate } = pricing.value
  const effective = formatMultiplier(rate.effective_rate)
  if (rate.independent) return t('modelChannelStatus.detail.pricing.rateNoteIndependent', { effective })
  if (rate.user_rate != null) {
    return t('modelChannelStatus.detail.pricing.rateNote', {
      effective,
      group: formatMultiplier(rate.group_rate),
      user: formatMultiplier(rate.user_rate),
    })
  }
  return t('modelChannelStatus.detail.pricing.rateNoteGroup', { effective })
})

function tokenLines(prices: CatalogPaidPrices): Line[] {
  const lines: Line[] = [
    { label: t('modelChannelStatus.detail.pricing.input'), value: perMillion(prices.input) },
    { label: t('modelChannelStatus.detail.pricing.output'), value: perMillion(prices.output) },
  ]
  if (prices.cache_read != null) {
    lines.push({ label: t('modelChannelStatus.detail.pricing.cacheRead'), value: perMillion(prices.cache_read) })
  }
  if (prices.cache_write != null) {
    lines.push({ label: t('modelChannelStatus.detail.pricing.cacheWrite'), value: perMillion(prices.cache_write) })
  }
  if (prices.cache_write_1h != null) {
    lines.push({ label: t('modelChannelStatus.detail.pricing.cacheWrite1h'), value: perMillion(prices.cache_write_1h) })
  }
  return lines
}

const paidLines = computed<Line[]>(() => {
  const p = pricing.value
  if (isToken.value) return tokenLines(p.paid)
  const lines: Line[] = []
  if (p.per_request != null) {
    const label =
      p.billing_mode === 'image'
        ? t('modelChannelStatus.detail.pricing.perImage')
        : p.billing_mode === 'video'
          ? t('modelChannelStatus.detail.pricing.perVideo')
          : t('modelChannelStatus.detail.pricing.perRequest')
    lines.push({ label, value: perUnit(p.per_request) })
  }
  if (p.image_input != null) {
    lines.push({ label: t('modelChannelStatus.detail.pricing.imageInput'), value: perUnit(p.image_input) })
  }
  if (p.image_output != null) {
    lines.push({ label: t('modelChannelStatus.detail.pricing.imageOutput'), value: perUnit(p.image_output) })
  }
  return lines
})

const anyPrice = computed(() => {
  const p = pricing.value
  if (isToken.value) return p.paid.input != null || p.paid.output != null
  return paidLines.value.length > 0 || (p.request_tiers?.some((tier) => tier.price != null) ?? false)
})

const officialLines = computed<Line[]>(() => {
  const official = pricing.value.official
  if (!official || !isToken.value) return []
  return tokenLines(official).filter((line) => line.value !== '-')
})

function tierRange(min: number, max: number | null): string {
  if (max == null) return t('modelChannelStatus.detail.pricing.tierOpen', { min: formatTokens(min) })
  return t('modelChannelStatus.detail.pricing.tierRange', { min: formatTokens(min), max: formatTokens(max) })
}

interface Tag {
  text: string
  title?: string
}

const tags = computed<Tag[]>(() => {
  const p = pricing.value
  const out: Tag[] = []
  if (p.peak) {
    out.push({
      text: t('modelChannelStatus.detail.pricing.peakTag', {
        window: `${p.peak.start}–${p.peak.end}`,
        multiplier: Number(p.peak.multiplier.toFixed(3)),
      }),
    })
  }
  if (p.time_pricing) {
    const title = t('modelChannelStatus.detail.pricing.timeTitle', {
      timezone: p.time_pricing.timezone,
      weekdays: p.time_pricing.weekdays_only ? t('modelChannelStatus.detail.pricing.weekdaysOnly') : '',
    })
    for (const period of p.time_pricing.periods) {
      out.push({
        text: t('modelChannelStatus.detail.pricing.timeTag', {
          start: period.start_time,
          end: period.end_time,
          multiplier: Number(period.multiplier.toFixed(3)),
        }),
        title,
      })
    }
  }
  for (const [effort, multiplier] of Object.entries(p.reasoning_effort_multipliers ?? {})) {
    out.push({
      text: t('modelChannelStatus.detail.pricing.reasoningTag', { effort, multiplier: Number(multiplier.toFixed(3)) }),
    })
  }
  return out
})
</script>
