<template>
  <div v-if="result.kind && result.kind !== 'probe'" class="mt-2 space-y-1 text-xs">
    <div class="flex flex-wrap items-center gap-2">
      <span
        v-if="result.verdict"
        :class="[
          'inline-flex items-center rounded-full px-2 py-0.5 font-medium',
          result.verdict === 'pass'
            ? 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400'
            : 'bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-400'
        ]"
        data-test="verdict"
      >
        {{ result.verdict === 'pass' ? t('admin.scheduledTestKinds.verdictPass') : t('admin.scheduledTestKinds.verdictFail') }}
      </span>
      <span v-if="result.model_id" class="font-mono text-gray-500 dark:text-gray-400">
        {{ t('admin.scheduledTestKinds.upstreamModel') }}: {{ result.model_id }}
      </span>
    </div>
    <template v-if="result.kind === 'quiz'">
      <p class="whitespace-pre-wrap text-gray-700 dark:text-gray-300">
        <span class="text-gray-400">{{ t('admin.scheduledTestKinds.question') }}:</span> {{ result.prompt }}
      </p>
      <p class="text-gray-700 dark:text-gray-300">
        <span class="text-gray-400">{{ t('admin.scheduledTestKinds.expectedAnswer') }}:</span> {{ result.expected_answer }}
      </p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { ScheduledTestResult } from '@/types'

defineProps<{
  result: ScheduledTestResult
}>()

const { t } = useI18n()
</script>
