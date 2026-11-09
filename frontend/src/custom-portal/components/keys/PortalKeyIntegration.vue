<template>
  <aside
    class="key-integration min-w-0 space-y-4 border-l border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800 xl:max-h-full xl:overflow-y-auto"
    data-testid="key-integration"
  >
    <header class="flex items-center justify-between gap-2">
      <h2 class="text-sm font-semibold">{{ pt('refresh.integration') }} · {{ apiKey.name }}</h2>
      <button
        type="button"
        class="btn btn-ghost btn-sm"
        :aria-label="translateGlobal('common.close')"
        @click="$emit('close')"
      >
        ×
      </button>
    </header>
    <div class="flex flex-wrap gap-2" role="group" :aria-label="pt('refresh.integration')">
      <button
        v-for="tab in tabs"
        :key="tab"
        type="button"
        class="border px-2 py-1 text-xs"
        :aria-pressed="activeTab === tab"
        :class="
          activeTab === tab ? 'bg-gray-900 text-white dark:bg-dark-100 dark:text-dark-900' : ''
        "
        @click="activeTab = tab"
      >
        {{ tab === 'clients' ? pt('refresh.clients') : tab === 'python' ? 'Python' : tab }}
      </button>
    </div>
    <UseKeyModal
      v-if="activeTab === 'clients'"
      :key="apiKey.id"
      embedded
      :show="true"
      :api-key="apiKey.key"
      :base-url="baseUrl"
      :platform="apiKey.group?.platform || null"
      :claude-code-only="apiKey.group?.claude_code_only || false"
      :allow-messages-dispatch="apiKey.group?.allow_messages_dispatch || false"
    />
    <template v-else
      ><p class="text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.keyEnvironment') }}</p>
      <pre
        class="max-h-96 overflow-auto whitespace-pre bg-gray-900 p-4 text-xs leading-6 text-gray-100 dark:bg-dark-950"
        >{{ examples[activeTab] }}</pre
      >
      <button
        type="button"
        class="btn btn-secondary btn-sm"
        @click="copyToClipboard(examples[activeTab])"
      >
        {{ copied ? pt('refresh.copied') : pt('refresh.copy') }}
      </button></template
    >
    <button
      v-if="!hideImport"
      type="button"
      class="btn btn-secondary w-full"
      @click="$emit('import', apiKey)"
    >
      {{ translateGlobal('keys.importToCcSwitch') }}
    </button>
  </aside>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import UseKeyModal from '@/components/keys/UseKeyModal.vue'
import type { ApiKey } from '@/types'
import { usePortalI18n } from '../../i18n'
import { useClipboard } from '@/composables/useClipboard'
import { integrationExamples } from './integrationExamples'
const props = defineProps<{ apiKey: ApiKey; baseUrl: string; hideImport?: boolean }>()
defineEmits<{ close: []; import: [key: ApiKey] }>()
const { t: translateGlobal } = useI18n()
const { pt } = usePortalI18n()
const { copied, copyToClipboard } = useClipboard()
const activeTab = ref<'clients' | 'curl' | 'python'>('clients')
const tabs = computed(() =>
  ['anthropic', 'openai', 'grok'].includes(props.apiKey.group?.platform || '')
    ? (['clients', 'curl', 'python'] as const)
    : (['clients'] as const)
)
const examples = computed(() =>
  integrationExamples(
    props.baseUrl.trim() || window.location.origin,
    props.apiKey.group?.platform === 'anthropic'
  )
)
</script>
<style scoped>
.key-integration > header { margin: -16px -16px 0; padding: 12px 16px; border-bottom: 1px solid var(--pp-divider); }
.key-integration > [role='group'] { border-bottom: 1px solid var(--pp-divider); padding-bottom: 12px; }
.key-integration > [role='group'] button { border: 0; border-bottom: 3px solid transparent; background: none; color: inherit; }
.key-integration > [role='group'] button[aria-pressed='true'] { border-bottom-color: var(--pp-ink); font-weight: 600; }
</style>
