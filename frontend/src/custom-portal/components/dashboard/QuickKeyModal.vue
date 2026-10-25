<template>
  <div v-if="show" class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm">
    <div class="relative w-full max-w-lg overflow-hidden rounded-2xl border border-gray-200/80 bg-white p-6 shadow-2xl dark:border-dark-700 dark:bg-dark-900">
      <!-- Close Button -->
      <button
        @click="close"
        class="absolute right-4 top-4 rounded-xl p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-800 dark:hover:text-white"
      >
        <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg>
      </button>

      <div class="mb-4">
        <div class="inline-flex items-center gap-1.5 rounded-full bg-primary-100/80 px-2.5 py-0.5 text-[11px] font-semibold text-primary-800 dark:bg-primary-950/60 dark:text-primary-300">
          {{ pt('quickKey.badge') }}
        </div>
        <h3 class="mt-2 text-lg font-bold text-gray-900 dark:text-white">
          {{ createdKey ? pt('quickKey.createdTitle') : pt('quickKey.createTitle') }}
        </h3>
        <p class="text-xs text-gray-500 dark:text-dark-400 mt-1">
          {{ createdKey ? pt('quickKey.createdHint') : pt('quickKey.createHint') }}
        </p>
      </div>

      <!-- Creation Form -->
      <form v-if="!createdKey" @submit.prevent="handleCreate" class="space-y-4">
        <div>
          <label for="quick-key-name" class="block text-xs font-bold text-gray-700 dark:text-gray-300 mb-1">{{ pt('quickKey.nameLabel') }}</label>
          <input
            id="quick-key-name"
            v-model="keyName"
            type="text"
            required
            :placeholder="pt('quickKey.namePlaceholder')"
            class="w-full rounded-xl border border-gray-200 bg-white px-3.5 py-2 text-xs text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-1 focus:ring-primary-500 dark:border-dark-700 dark:bg-dark-950 dark:text-white"
          />
        </div>

        <div>
          <label for="quick-key-group" class="block text-xs font-bold text-gray-700 dark:text-gray-300 mb-1">{{ pt('quickKey.groupLabel') }}</label>
          <p v-if="groupsStatus === 'loading'" class="text-[11px] text-gray-500 dark:text-dark-400">
            {{ pt('quickKey.groupsLoading') }}
          </p>
          <p v-else-if="groupsStatus === 'error' || groups.length === 0" data-testid="quick-key-no-groups" class="rounded-xl border border-amber-500/30 bg-amber-50/60 p-3 text-[11px] text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">
            {{ groupsStatus === 'error' ? pt('quickKey.groupsLoadFailed') : pt('quickKey.noGroups') }}
            <router-link to="/keys" class="font-semibold underline" @click="close">{{ pt('quickKey.keysPageLink') }}</router-link>
            {{ pt('quickKey.noGroupsTail') }}
          </p>
          <select
            v-else
            id="quick-key-group"
            v-model="groupId"
            required
            class="w-full rounded-xl border border-gray-200 bg-white px-3.5 py-2 text-xs text-gray-900 focus:border-primary-500 focus:outline-none focus:ring-1 focus:ring-primary-500 dark:border-dark-700 dark:bg-dark-950 dark:text-white"
          >
            <option :value="null" disabled>{{ pt('quickKey.groupPlaceholder') }}</option>
            <option v-for="group in groups" :key="group.id" :value="group.id">
              {{ group.name }} · {{ group.platform }} · x{{ group.rate_multiplier }}
            </option>
          </select>
        </div>

        <div class="rounded-xl border border-gray-200/60 bg-gray-50/60 p-3 text-[11px] text-gray-500 dark:border-dark-800 dark:bg-dark-950/60 dark:text-dark-400">
          {{ pt('quickKey.defaultsNotice') }}
        </div>

        <div class="flex items-center justify-end gap-2 pt-2">
          <button
            type="button"
            @click="close"
            class="rounded-xl border border-gray-200 px-4 py-2 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:border-dark-700 dark:text-dark-300 dark:hover:bg-dark-800"
          >
            {{ pt('common.cancel') }}
          </button>
          <button
            type="submit"
            :disabled="submitting || !keyName.trim() || groupId === null"
            class="inline-flex items-center gap-1.5 rounded-xl bg-gradient-to-r from-primary-600 to-primary-500 px-5 py-2 text-xs font-bold text-white shadow-md shadow-primary-500/20 hover:from-primary-500 hover:to-primary-600 disabled:opacity-60"
          >
            <svg v-if="submitting" class="h-3.5 w-3.5 animate-spin" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z"></path></svg>
            <span>{{ submitting ? pt('quickKey.submitting') : pt('quickKey.submit') }}</span>
          </button>
        </div>
      </form>

      <!-- Created Success View -->
      <div v-else class="space-y-4">
        <div>
          <label class="block text-xs font-bold text-gray-700 dark:text-gray-300 mb-1">{{ pt('quickKey.keyLabel') }}</label>
          <div class="flex items-center gap-2">
            <input
              type="text"
              readonly
              :value="createdKey.key"
              class="w-full rounded-xl border border-emerald-500/50 bg-emerald-50/40 px-3.5 py-2 font-mono text-xs font-bold text-emerald-800 dark:border-emerald-500/30 dark:bg-emerald-950/20 dark:text-emerald-300"
            />
            <button
              @click="copyKey"
              class="shrink-0 rounded-xl bg-emerald-600 px-3.5 py-2 text-xs font-bold text-white hover:bg-emerald-500"
            >
              {{ copied ? pt('common.copied') : pt('common.copy') }}
            </button>
          </div>
        </div>

        <!-- Quick Code Setup -->
        <div>
          <div class="mb-1 text-xs font-bold text-gray-700 dark:text-gray-300">{{ pt('quickKey.snippetTitle') }}</div>
          <CodeSnippet
            :code="exportSnippet"
            language="bash"
            :title="pt('quickKey.snippetFile')"
          />
        </div>

        <div class="flex justify-end pt-2">
          <button
            @click="close"
            class="rounded-xl bg-gray-900 px-5 py-2 text-xs font-bold text-white hover:bg-gray-800 dark:bg-white dark:text-gray-900 dark:hover:bg-gray-100"
          >
            {{ pt('quickKey.done') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { create as createApiKey } from '@/api/keys'
import { getAvailable as getAvailableGroups } from '@/api/groups'
import type { ApiKey, Group } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import CodeSnippet from '../common/CodeSnippet.vue'
import { useAppStore } from '@/stores'
import { usePortalI18n } from '../../i18n'

type GroupsStatus = 'idle' | 'loading' | 'ready' | 'error'

const props = defineProps<{
  show: boolean
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'created'): void
}>()

const { pt } = usePortalI18n()
const appStore = useAppStore()
const baseUrl = computed(() => appStore.cachedPublicSettings?.api_base_url || window.location.origin)

const DEFAULT_KEY_NAME = 'Dev-Client'

const keyName = ref(DEFAULT_KEY_NAME)
const groupId = ref<number | null>(null)
const groups = ref<Group[]>([])
const groupsStatus = ref<GroupsStatus>('idle')
const submitting = ref(false)
const createdKey = ref<ApiKey | null>(null)
const copied = ref(false)

const exportSnippet = computed(() => {
  const k = createdKey.value?.key || 'sk-sub2api-...'
  return `export ANTHROPIC_BASE_URL="${baseUrl.value}"
export ANTHROPIC_API_KEY="${k}"`
})

async function loadGroups() {
  if (groupsStatus.value === 'loading' || groupsStatus.value === 'ready') return
  groupsStatus.value = 'loading'
  try {
    groups.value = await getAvailableGroups()
    groupsStatus.value = 'ready'
    if (groups.value.length === 1) {
      groupId.value = groups.value[0].id
    }
  } catch (err) {
    console.error('Failed to load groups:', err)
    groupsStatus.value = 'error'
  }
}

watch(() => props.show, (visible) => {
  if (visible) loadGroups()
}, { immediate: true })

async function handleCreate() {
  const name = keyName.value.trim()
  if (!name) return
  if (groupId.value === null) {
    appStore.showError(pt('quickKey.groupRequired'))
    return
  }
  submitting.value = true
  try {
    createdKey.value = await createApiKey(name, groupId.value)
    emit('created')
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, pt('quickKey.createFailed')))
  } finally {
    submitting.value = false
  }
}

async function copyKey() {
  if (!createdKey.value?.key) return
  try {
    await navigator.clipboard.writeText(createdKey.value.key)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch (err) {
    console.error(err)
  }
}

function close() {
  createdKey.value = null
  keyName.value = DEFAULT_KEY_NAME
  copied.value = false
  if (groupsStatus.value === 'error') groupsStatus.value = 'idle'
  emit('close')
}
</script>
