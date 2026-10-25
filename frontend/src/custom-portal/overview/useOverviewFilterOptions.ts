import { computed, ref, shallowRef, watch, type ComputedRef, type Ref } from 'vue'
import { userGroupsAPI } from '@/api/groups'
import { keysAPI } from '@/api/keys'
import type { ApiKey, UsageRequestType } from '@/types'
import { injectOverviewState, type OverviewState } from './usageChartState'

export interface FilterOption<T> {
  value: T
  label: string
}

export interface RequestTypeOption {
  value: UsageRequestType
  /** Existing upstream key, the same one `UsageView` shows. */
  labelKey: string
}

/** The request types `UsageView` offers, in its order. */
export const REQUEST_TYPE_OPTIONS: readonly RequestTypeOption[] = [
  { value: 'ws_v2', labelKey: 'usage.ws' },
  { value: 'live', labelKey: 'usage.live' },
  { value: 'stream', labelKey: 'usage.stream' },
  { value: 'sync', labelKey: 'usage.sync' }
]

export interface OverviewFilterOptions {
  apiKeys: Readonly<Ref<FilterOption<number>[]>>
  apiKeysLoading: Readonly<Ref<boolean>>
  apiKeysLoaded: Readonly<Ref<boolean>>
  apiKeysError: Readonly<Ref<boolean>>
  /** Fetches every page of the user's keys and their available groups once; a failure allows a retry. */
  loadApiKeys: () => Promise<void>
  /** Every model seen this session, so applying a model filter never empties the list. */
  modelOptions: ComputedRef<FilterOption<string>[]>
  /** The groups the user can use; usage logs only carry a group id when it is positive. */
  groupOptions: ComputedRef<FilterOption<number>[]>
  requestTypeOptions: readonly RequestTypeOption[]
}

const KEY_PAGE_SIZE = 100

async function fetchAllApiKeys(): Promise<ApiKey[]> {
  const firstPage = await keysAPI.list(1, KEY_PAGE_SIZE)
  const keys = [...firstPage.items]
  for (let page = 2; page <= firstPage.pages; page++) {
    const response = await keysAPI.list(page, KEY_PAGE_SIZE)
    if (!response.items.length) break
    keys.push(...response.items)
  }
  return keys
}

const byLabel = <T>(a: FilterOption<T>, b: FilterOption<T>) => a.label.localeCompare(b.label)

export function useOverviewFilterOptions(state: OverviewState = injectOverviewState()): OverviewFilterOptions {
  const apiKeys = shallowRef<FilterOption<number>[]>([])
  const apiKeysLoading = ref(false)
  const apiKeysLoaded = ref(false)
  const apiKeysError = ref(false)
  const availableGroups = shallowRef<FilterOption<number>[]>([])
  let pending: Promise<void> | null = null

  function loadApiKeys(): Promise<void> {
    if (apiKeysLoaded.value) return Promise.resolve()
    if (pending) return pending
    apiKeysLoading.value = true
    pending = Promise.all([fetchAllApiKeys(), userGroupsAPI.getAvailable()])
      .then(([keys, groups]) => {
        apiKeys.value = keys.map((key) => ({ value: key.id, label: key.name || `#${key.id}` }))
        availableGroups.value = groups
          .filter((group) => group.id > 0)
          .map((group) => ({ value: group.id, label: group.name || `#${group.id}` }))
        apiKeysLoaded.value = true
        apiKeysError.value = false
      })
      .catch((error: unknown) => {
        console.error('Failed to load overview API keys:', error)
        apiKeysError.value = true
      })
      .finally(() => {
        apiKeysLoading.value = false
        pending = null
      })
    return pending
  }

  const seenModels = ref(new Set<string>())

  watch(
    state.models,
    (models) => {
      for (const stat of models) if (stat.model) seenModels.value.add(stat.model)
    },
    { immediate: true }
  )

  const modelOptions = computed(() => {
    const names = new Set(seenModels.value)
    if (state.filters.value.model) names.add(state.filters.value.model)
    return [...names].map((name) => ({ value: name, label: name })).sort(byLabel)
  })

  const groupOptions = computed(() => {
    const entries = new Map(availableGroups.value.map((option) => [option.value, option.label]))
    const selected = state.filters.value.groupId
    if (selected !== undefined && !entries.has(selected)) entries.set(selected, `#${selected}`)
    return [...entries].map(([value, label]) => ({ value, label })).sort(byLabel)
  })

  return {
    apiKeys,
    apiKeysLoading,
    apiKeysLoaded,
    apiKeysError,
    loadApiKeys,
    modelOptions,
    groupOptions,
    requestTypeOptions: REQUEST_TYPE_OPTIONS
  }
}
