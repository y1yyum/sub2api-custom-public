<template>
  <AppLayout>
    <div class="w-full min-w-0 space-y-6 pb-8">
      <header
        class="page-header mb-0 rounded-3xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6"
      >
        <h1 class="page-title text-xl font-black text-gray-900 dark:text-white">{{ t('admin.scheduledTestKinds.page.title') }}</h1>
        <p class="page-description mt-1.5 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.scheduledTestKinds.page.description') }}
        </p>
        <div class="mt-4 border-t border-gray-100 pt-4 dark:border-dark-700">
          <div class="tabs inline-flex w-full max-w-xl flex-wrap sm:w-auto" role="tablist">
            <button
              v-for="option in TABS"
              :key="option"
              type="button"
              role="tab"
              class="tab flex-1 sm:flex-none"
              :class="tab === option ? 'tab-active' : ''"
              :aria-selected="tab === option"
              :data-tab="option"
              @click="tab = option"
            >
              {{ t(`admin.scheduledTestKinds.page.tabs.${option}`) }}
            </button>
          </div>
        </div>
      </header>

      <section v-if="tab === 'questions'" class="card p-5">
        <IntelligenceQuestionBank />
      </section>

      <TablePageLayout v-else>
        <template #filters>
          <div class="flex flex-wrap items-center gap-3">
            <div class="w-full sm:w-64">
              <Input v-model="search" :placeholder="t('admin.scheduledTestKinds.page.search')" />
            </div>
            <div class="w-40">
              <Select v-model="kindFilter" :options="kindOptions" />
            </div>
            <div class="flex-1" />
            <button type="button" class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="load">
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" :stroke-width="2" />
            </button>
            <button type="button" class="btn btn-primary flex items-center gap-1.5" data-test="add-plan" @click="openPicker">
              <Icon name="plus" size="sm" :stroke-width="2" />
              {{ t('admin.scheduledTests.addPlan') }}
            </button>
          </div>
        </template>

        <template #table>
          <DataTable :columns="columns" :data="visiblePlans" :loading="loading">
            <template #cell-account_name="{ row }">
              <div class="min-w-0">
                <div class="truncate font-medium text-gray-900 dark:text-white">{{ row.account_name }}</div>
                <div class="text-xs text-gray-500 dark:text-gray-400">{{ platformLabel(row.account_platform) }}</div>
              </div>
            </template>

            <template #cell-kind="{ row }">
              <span class="inline-flex rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-300">
                {{ t(`admin.scheduledTestKinds.kinds.${row.kind}`) }}
              </span>
            </template>

            <template #cell-model_id="{ value }">
              <span class="font-mono text-xs text-gray-700 dark:text-gray-300">{{ value }}</span>
            </template>

            <template #cell-cron_expression="{ value }">
              <span class="font-mono text-xs text-gray-700 dark:text-gray-300">{{ value }}</span>
            </template>

            <template #cell-recent="{ row }">
              <div v-if="row.recent.length" class="space-y-1" :data-recent="row.id">
                <div class="flex gap-0.5">
                  <span
                    v-for="(run, index) in (row as IntelligencePlanOverview).recent"
                    :key="index"
                    :class="['h-3 w-1.5 rounded-sm', OUTCOME_CLASS[run.outcome]]"
                    :title="`${t(`admin.scheduledTestKinds.page.outcomes.${run.outcome}`)} · ${formatDateTime(run.checked_at)}`"
                    :data-outcome="run.outcome"
                  />
                </div>
                <div class="text-xs text-gray-500 dark:text-gray-400">{{ recentSummary(row) }}</div>
              </div>
              <span v-else class="text-xs text-gray-400">{{ t('admin.scheduledTestKinds.page.noRuns') }}</span>
            </template>

            <template #cell-last_run_at="{ value }">
              <span class="text-xs text-gray-600 dark:text-gray-400">{{ value ? formatDateTime(value) : '-' }}</span>
            </template>

            <template #cell-next_run_at="{ row, value }">
              <span class="text-xs text-gray-600 dark:text-gray-400">{{ row.enabled && value ? formatDateTime(value) : '-' }}</span>
            </template>

            <template #cell-enabled="{ row }">
              <Toggle :model-value="row.enabled" @update:model-value="(enabled: boolean) => setEnabled(row, enabled)" />
            </template>

            <template #cell-actions="{ row }">
              <div class="flex items-center gap-1">
                <button
                  type="button"
                  class="rounded-lg p-1.5 text-gray-400 hover:bg-blue-50 hover:text-blue-500 dark:hover:bg-blue-900/20"
                  :title="t('admin.scheduledTests.editPlan')"
                  data-test="edit-plan"
                  @click="openPanel(row.account_id, { editPlanId: row.id })"
                >
                  <Icon name="edit" size="sm" :stroke-width="2" />
                </button>
                <button
                  type="button"
                  class="rounded-lg p-1.5 text-gray-400 hover:bg-red-50 hover:text-red-500 dark:hover:bg-red-900/20"
                  :title="t('admin.scheduledTests.deletePlan')"
                  data-test="delete-plan"
                  @click="deleting = row"
                >
                  <Icon name="trash" size="sm" :stroke-width="2" />
                </button>
              </div>
            </template>

            <template #empty>
              <EmptyState
                :title="t('admin.scheduledTestKinds.page.empty')"
                :action-text="t('admin.scheduledTests.addPlan')"
                @action="openPicker"
              />
            </template>
          </DataTable>
        </template>
      </TablePageLayout>
    </div>

    <ScheduledTestsPanel
      :show="panel.show"
      :account-id="panel.accountId"
      :model-options="panel.modelOptions"
      :edit-plan-id="panel.editPlanId"
      :add-kind="panel.addKind"
      @close="closePanel"
    />

    <BaseDialog :show="picker.show" :title="t('admin.scheduledTestKinds.page.pickAccount')" width="narrow" @close="picker.show = false">
      <div class="space-y-4">
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.scheduledTestKinds.page.pickAccountHint') }}</p>
        <Select
          v-model="picker.accountId"
          :options="picker.options"
          :placeholder="t('admin.scheduledTestKinds.page.account')"
          :loading="picker.loading"
          searchable
          remote
          @search="searchAccounts"
        />
        <div class="inline-flex rounded-lg border border-gray-200 p-0.5 dark:border-dark-600" role="radiogroup">
          <button
            v-for="kind in KINDS"
            :key="kind"
            type="button"
            role="radio"
            :aria-checked="picker.kind === kind"
            :data-kind="kind"
            :class="[
              'rounded-md px-3 py-1 text-xs font-medium transition-colors',
              picker.kind === kind ? 'bg-primary-500 text-white' : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700'
            ]"
            @click="picker.kind = kind"
          >
            {{ t(`admin.scheduledTestKinds.kinds.${kind}`) }}
          </button>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="picker.show = false">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary" :disabled="!picker.accountId" data-test="picker-next" @click="confirmPicker">
            {{ t('admin.scheduledTestKinds.page.next') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="deleting !== null"
      :title="t('admin.scheduledTests.deletePlan')"
      :message="t('admin.scheduledTests.confirmDelete')"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="deleting = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Input from '@/components/common/Input.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import { Icon } from '@/components/icons'
import IntelligenceQuestionBank from '@/components/admin/account/IntelligenceQuestionBank.vue'
import ScheduledTestsPanel from '@/components/admin/account/ScheduledTestsPanel.vue'
import { adminAPI } from '@/api/admin'
import {
  intelligenceQuestionsAPI,
  type IntelligencePlanOverview,
  type IntelligenceRunOutcome
} from '@/api/admin/intelligenceQuestions'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { platformLabel } from '@/utils/platformColors'
import type { Column } from '@/components/common/types'
import type { ScheduledTestKind } from '@/types'

type Tab = 'plans' | 'questions'
type IntelligenceKind = Extract<ScheduledTestKind, 'quiz' | 'char_art'>

const TABS: Tab[] = ['plans', 'questions']
const KINDS: IntelligenceKind[] = ['quiz', 'char_art']
const OUTCOME_CLASS: Record<IntelligenceRunOutcome, string> = {
  pass: 'bg-green-500',
  success: 'bg-green-500',
  fail: 'bg-amber-400',
  error: 'bg-red-500'
}

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const appStore = useAppStore()

const tab = computed<Tab>({
  get: () => (route.query.tab === 'questions' ? 'questions' : 'plans'),
  set: (value) => {
    void router.replace({ query: { ...route.query, tab: value === 'plans' ? undefined : value } })
  }
})

const loading = ref(false)
const plans = ref<IntelligencePlanOverview[]>([])
const search = ref('')
const kindFilter = ref<'' | IntelligenceKind>('')
const deleting = ref<IntelligencePlanOverview | null>(null)

const kindOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.scheduledTestKinds.page.allKinds') },
  ...KINDS.map((kind) => ({ value: kind, label: t(`admin.scheduledTestKinds.kinds.${kind}`) }))
])

const columns = computed<Column[]>(() => [
  { key: 'account_name', label: t('admin.scheduledTestKinds.page.account') },
  { key: 'kind', label: t('admin.scheduledTestKinds.type') },
  { key: 'model_id', label: t('admin.scheduledTests.model') },
  { key: 'cron_expression', label: t('admin.scheduledTests.cronExpression') },
  { key: 'recent', label: t('admin.scheduledTestKinds.page.recent') },
  { key: 'last_run_at', label: t('admin.scheduledTests.lastRun') },
  { key: 'next_run_at', label: t('admin.scheduledTests.nextRun') },
  { key: 'enabled', label: t('admin.scheduledTests.enabled') },
  { key: 'actions', label: t('common.actions') }
])

const visiblePlans = computed(() => {
  const query = search.value.trim().toLowerCase()
  return plans.value.filter((plan) =>
    (!kindFilter.value || plan.kind === kindFilter.value) &&
    (!query || plan.account_name.toLowerCase().includes(query) || plan.model_id.toLowerCase().includes(query))
  )
})

const recentSummary = (plan: IntelligencePlanOverview) => {
  if (plan.kind === 'quiz') {
    const scored = plan.recent.filter((run) => run.outcome !== 'error')
    const passed = scored.filter((run) => run.outcome === 'pass').length
    return t('admin.scheduledTestKinds.page.passed', { passed, total: scored.length })
  }
  const passed = plan.recent.filter((run) => run.outcome === 'success').length
  return t('admin.scheduledTestKinds.page.succeeded', { passed, total: plan.recent.length })
}

const load = async () => {
  loading.value = true
  try {
    plans.value = await intelligenceQuestionsAPI.listPlans()
  } catch (error: any) {
    appStore.showError(error?.message || 'Failed to load plans')
  } finally {
    loading.value = false
  }
}

onMounted(load)

const setEnabled = async (plan: IntelligencePlanOverview, enabled: boolean) => {
  try {
    const updated = await adminAPI.scheduledTests.update(plan.id, { enabled })
    plans.value = plans.value.map((p) => (p.id === plan.id ? { ...p, ...updated } : p))
    appStore.showSuccess(t('admin.scheduledTests.updateSuccess'))
  } catch (error: any) {
    appStore.showError(error?.message || 'Failed to update plan')
  }
}

const confirmDelete = async () => {
  const target = deleting.value
  deleting.value = null
  if (!target) return
  try {
    await adminAPI.scheduledTests.delete(target.id)
    plans.value = plans.value.filter((p) => p.id !== target.id)
    appStore.showSuccess(t('admin.scheduledTests.deleteSuccess'))
  } catch (error: any) {
    appStore.showError(error?.message || 'Failed to delete plan')
  }
}

const panel = reactive({
  show: false,
  accountId: null as number | null,
  modelOptions: [] as SelectOption[],
  editPlanId: null as number | null,
  addKind: null as IntelligenceKind | null
})

const openPanel = async (accountId: number, focus: { editPlanId?: number; addKind?: IntelligenceKind }) => {
  Object.assign(panel, { show: true, accountId, modelOptions: [], editPlanId: focus.editPlanId ?? null, addKind: focus.addKind ?? null })
  try {
    const models = await adminAPI.accounts.getAvailableModels(accountId)
    if (panel.accountId === accountId) {
      panel.modelOptions = models.map((m) => ({ value: m.id, label: m.display_name || m.id }))
    }
  } catch (error: any) {
    appStore.showError(error?.message || 'Failed to load models')
  }
}

const closePanel = () => {
  Object.assign(panel, { show: false, accountId: null, modelOptions: [], editPlanId: null, addKind: null })
  void load()
}

const picker = reactive({
  show: false,
  loading: false,
  accountId: null as number | null,
  kind: 'quiz' as IntelligenceKind,
  options: [] as SelectOption[]
})
let pickerRequestId = 0

const searchAccounts = async (query: string) => {
  const requestId = ++pickerRequestId
  picker.loading = true
  try {
    const page = await adminAPI.accounts.list(1, 20, { search: query || undefined, lite: '1' })
    if (requestId !== pickerRequestId) return
    const options = page.items.map((account) => ({ value: account.id, label: `${account.name} · ${platformLabel(account.platform)}` }))
    const selected = picker.options.find((option) => option.value === picker.accountId)
    picker.options = selected && !options.some((option) => option.value === selected.value) ? [selected, ...options] : options
  } catch (error: any) {
    appStore.showError(error?.message || 'Failed to load accounts')
  } finally {
    if (requestId === pickerRequestId) picker.loading = false
  }
}

const openPicker = () => {
  Object.assign(picker, { show: true, accountId: null, kind: 'quiz', options: [] })
  void searchAccounts('')
}

const confirmPicker = () => {
  if (!picker.accountId) return
  picker.show = false
  void openPanel(picker.accountId, { addKind: picker.kind })
}
</script>
