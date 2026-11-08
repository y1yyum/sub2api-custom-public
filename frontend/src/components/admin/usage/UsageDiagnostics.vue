<template>
  <div class="mb-1 mt-1 flex flex-wrap items-center gap-1 text-[10px]" data-testid="usage-diagnostics">
    <template v-if="diagnostics">
      <span v-if="diagnostics.display_scaled === true" class="rounded bg-amber-100 px-1 py-px text-amber-800 dark:bg-amber-500/15 dark:text-amber-300" :title="t('admin.usage.diagnostics.scaledHint')">
        {{ t('admin.usage.diagnostics.scaled') }}
      </span>
      <span v-if="diagnostics.input_change_rate != null && diagnostics.input_change_rate !== 0" class="rounded bg-violet-100 px-1 py-px tabular-nums text-violet-800 dark:bg-violet-500/15 dark:text-violet-300" :title="t('admin.usage.diagnostics.changeHint')">
        {{ changeRate }}
      </span>
      <span v-if="diagnostics.stop_reason" class="max-w-28 truncate rounded bg-gray-100 px-1 py-px text-gray-600 dark:bg-dark-700 dark:text-gray-300" :title="diagnostics.stop_reason">
        {{ stopLabel }}
      </span>
      <span :class="diagnostics.stream_complete ? 'text-emerald-600 dark:text-emerald-400' : 'text-rose-600 dark:text-rose-400'" :title="integrityLabel" :aria-label="integrityLabel">
        {{ diagnostics.stream_complete ? '✓' : '!' }}
      </span>
    </template>
    <button type="button" class="rounded px-1 py-px text-primary-600 underline decoration-dotted underline-offset-2 hover:bg-primary-50 focus-visible:outline focus-visible:outline-primary-500 dark:text-primary-400 dark:hover:bg-primary-900/20" :aria-label="t('admin.usage.diagnostics.title')" @click="open = true">
      {{ t('admin.usage.diagnostics.details') }}
    </button>
  </div>

  <BaseDialog v-if="open" :show="open" :title="t('admin.usage.diagnostics.title')" width="wide" @close="open = false">
    <p v-if="!diagnostics" class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.usage.diagnostics.notRecorded') }}</p>
    <div v-else class="space-y-5 text-sm">
      <p class="rounded-lg bg-amber-50 p-3 text-xs leading-relaxed text-amber-900 dark:bg-amber-500/10 dark:text-amber-200">{{ t('admin.usage.diagnostics.upstreamHint') }}</p>

      <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
        <table class="w-full text-left text-xs">
          <thead class="bg-gray-50 dark:bg-dark-800"><tr>
            <th class="px-3 py-2 font-medium">{{ t('admin.usage.diagnostics.usage') }}</th>
            <th class="px-3 py-2 text-right font-medium">{{ t('admin.usage.diagnostics.initial') }}</th>
            <th class="px-3 py-2 text-right font-medium">{{ t('admin.usage.diagnostics.final') }}</th>
          </tr></thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="field in tokenFields" :key="field.key">
              <td class="px-3 py-2 text-gray-500 dark:text-gray-400">{{ t(field.label) }}</td>
              <td class="px-3 py-2 text-right tabular-nums">{{ tokenValue(diagnostics.initial_usage, field.key) }}</td>
              <td class="px-3 py-2 text-right tabular-nums">{{ tokenValue(diagnostics.final_usage, field.key) }}</td>
            </tr>
            <tr>
              <td class="px-3 py-2 text-gray-500 dark:text-gray-400">{{ t('admin.usage.diagnostics.scaled') }}</td>
              <td class="px-3 py-2 text-right">{{ flagValue(diagnostics.initial_usage?.display_scaled) }}</td>
              <td class="px-3 py-2 text-right">{{ flagValue(diagnostics.final_usage?.display_scaled) }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <dl class="grid grid-cols-1 gap-x-6 gap-y-3 sm:grid-cols-2">
        <div v-for="item in metadata" :key="item.label" class="min-w-0">
          <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t(item.label) }}</dt>
          <dd class="mt-1 break-all font-medium text-gray-900 dark:text-gray-100">{{ item.value }}</dd>
        </div>
      </dl>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.usage.diagnostics.integrityHint') }}</p>

      <section v-if="diagnostics.tool_calls?.length">
        <h4 class="mb-2 font-medium">{{ t('admin.usage.diagnostics.tools') }}</h4>
        <div class="flex flex-wrap gap-2">
          <span v-for="tool in diagnostics.tool_calls" :key="`${tool.type}:${tool.name}`" class="rounded-lg bg-gray-100 px-2 py-1 text-xs dark:bg-dark-700">
            {{ tool.name || unknown }} · {{ t(tool.type === 'server_tool_use' ? 'admin.usage.diagnostics.serverTool' : 'admin.usage.diagnostics.clientTool') }} × {{ tool.count }}
          </span>
        </div>
      </section>

      <section v-if="diagnostics.iterations?.length">
        <h4 class="mb-2 font-medium">{{ t('admin.usage.diagnostics.iterations') }} · {{ diagnostics.iteration_count }}</h4>
        <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
          <table class="w-full text-left text-xs">
            <thead class="bg-gray-50 dark:bg-dark-800"><tr>
              <th class="px-3 py-2"># / {{ t('admin.usage.diagnostics.kind') }}</th>
              <th class="px-3 py-2">{{ t('admin.usage.diagnostics.model') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.usage.diagnostics.totalInput') }}</th>
              <th class="px-3 py-2 text-right">{{ t('admin.usage.outputTokens') }}</th>
            </tr></thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="(iteration, index) in diagnostics.iterations" :key="index">
                <td class="px-3 py-2">{{ index + 1 }} / {{ iteration.type }}</td>
                <td class="max-w-52 break-all px-3 py-2">{{ iteration.model || unknown }}</td>
                <td class="px-3 py-2 text-right tabular-nums">{{ numberValue(iteration.usage?.total_input_tokens) }}</td>
                <td class="px-3 py-2 text-right tabular-nums">{{ numberValue(iteration.usage?.output_tokens) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="diagnostics.iterations_truncated" class="mt-2 text-xs text-gray-500">{{ t('admin.usage.diagnostics.truncated') }}</p>
      </section>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { UsageResponseDiagnostics, ResponseTokenUsage } from '@/types'

const props = defineProps<{ diagnostics?: UsageResponseDiagnostics | null }>()
const { t } = useI18n()
const open = ref(false)
const unknown = computed(() => t('admin.usage.diagnostics.notReturned'))
const numberValue = (value?: number | null) => value == null ? unknown.value : value.toLocaleString()
const flagValue = (value?: boolean | null) => value == null ? unknown.value : t(value ? 'admin.usage.diagnostics.yes' : 'admin.usage.diagnostics.no')
const changeRate = computed(() => {
  const rate = props.diagnostics?.input_change_rate
  return rate == null ? unknown.value : `${rate > 0 ? '+' : ''}${(rate * 100).toFixed(2)}%`
})
const stopLabels: Record<string, string> = {
  end_turn: 'ended', stop_sequence: 'stopSequence', tool_use: 'toolStop',
  max_tokens: 'outputLimit', model_context_window_exceeded: 'contextLimit', pause_turn: 'paused',
  stop: 'ended', length: 'outputLimit', tool_calls: 'toolStop', content_filter: 'filtered',
  STOP: 'ended', MAX_TOKENS: 'outputLimit',
  completed: 'ended', max_output_tokens: 'outputLimit',
}
const stopLabel = computed(() => {
  const reason = props.diagnostics?.stop_reason || ''
  return stopLabels[reason] ? t(`admin.usage.diagnostics.${stopLabels[reason]}`) : reason
})
const integrityLabel = computed(() => t(props.diagnostics?.stream_complete ? 'admin.usage.diagnostics.complete' : 'admin.usage.diagnostics.incomplete'))
type TokenKey = Exclude<keyof ResponseTokenUsage, 'display_scaled'>
const tokenFields: { key: TokenKey; label: string }[] = [
  { key: 'input_tokens', label: 'admin.usage.inputTokens' },
  { key: 'cache_read_tokens', label: 'admin.usage.cacheReadTokens' },
  { key: 'cache_creation_tokens', label: 'admin.usage.cacheCreationTokens' },
  { key: 'cache_creation_5m_tokens', label: 'admin.usage.diagnostics.cache5m' },
  { key: 'cache_creation_1h_tokens', label: 'admin.usage.diagnostics.cache1h' },
  { key: 'total_input_tokens', label: 'admin.usage.diagnostics.totalInput' },
  { key: 'output_tokens', label: 'admin.usage.outputTokens' },
]
const tokenValue = (usage: ResponseTokenUsage | null | undefined, key: TokenKey) => numberValue(usage?.[key])
const metadata = computed(() => {
  const d = props.diagnostics
  if (!d) return []
  return [
    { label: 'admin.usage.diagnostics.messageId', value: d.message_id || unknown.value },
    { label: 'admin.usage.diagnostics.protocol', value: d.protocol },
    { label: 'admin.usage.diagnostics.stopReason', value: d.stop_reason || unknown.value },
    { label: 'admin.usage.diagnostics.stopSequence', value: d.stop_sequence == null ? unknown.value : JSON.stringify(d.stop_sequence) },
    { label: 'admin.usage.diagnostics.integrity', value: integrityLabel.value },
    { label: 'admin.usage.diagnostics.change', value: changeRate.value },
    { label: 'admin.usage.diagnostics.thinking', value: numberValue(d.thinking_tokens) },
    { label: 'admin.usage.diagnostics.serviceTier', value: d.service_tier || unknown.value },
    { label: 'admin.usage.diagnostics.inferenceGeo', value: !d.inference_geo || d.inference_geo === 'not_available' ? unknown.value : d.inference_geo },
    { label: 'admin.usage.diagnostics.iterations', value: numberValue(d.iteration_count) },
    { label: 'admin.usage.diagnostics.events', value: numberValue(d.event_count) },
    { label: 'admin.usage.diagnostics.blocks', value: Object.entries(d.content_block_counts || {}).map(([kind, count]) => `${kind} × ${count}`).join(' · ') || unknown.value },
    { label: 'admin.usage.diagnostics.error', value: d.error_type || unknown.value },
  ]
})
</script>
