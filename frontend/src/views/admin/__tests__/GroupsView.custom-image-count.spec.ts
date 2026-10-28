import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AdminGroup } from '@/types'
import GroupsView from '@/views/admin/GroupsView.vue'
import { adminAPI } from '@/api/admin'

const {
  listGroups,
  duplicateGroup,
  updateGroup,
  getModelAllowlistCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  showSuccess,
  showError
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  duplicateGroup: vi.fn(),
  updateGroup: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

const authState = vi.hoisted(() => ({ isSimpleMode: false }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      duplicate: duplicateGroup,
      getModelAllowlistCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      getAll: vi.fn(),
      create: vi.fn(),
      update: updateGroup,
      delete: vi.fn(),
      updateSortOrder: vi.fn()
    },
    accounts: {
      list: vi.fn(),
      getById: vi.fn()
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep: vi.fn(() => false),
    nextStep: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const sourceGroup: AdminGroup = {
  id: 42,
  name: 'Primary',
  description: null,
  platform: 'openai',
  rate_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  status: 'active',
  subscription_type: 'standard',
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
  allow_image_generation: true,
  image_count_billing: true,
  allow_batch_image_generation: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  batch_image_discount_multiplier: 0.5,
  batch_image_hold_multiplier: 0.6,
  image_price_1k: null,
  image_price_2k: null,
  image_price_4k: null,
  video_rate_independent: false,
  video_rate_multiplier: 1,
  video_price_480p: null,
  video_price_720p: null,
  video_price_1080p: null,
  web_search_price_per_call: null,
  peak_rate_enabled: false,
  peak_start: '',
  peak_end: '',
  peak_rate_multiplier: 1,
  claude_code_only: false,
  fallback_group_id: null,
  fallback_group_id_on_invalid_request: null,
  allow_messages_dispatch: false,
  default_mapped_model: '',
  messages_dispatch_model_config: undefined,
  require_oauth_only: false,
  require_privacy_set: false,
  created_at: '2026-07-16T00:00:00Z',
  updated_at: '2026-07-16T00:00:00Z',
  model_routing: null,
  model_routing_enabled: false,
  mcp_xml_inject: true,
  supported_model_scopes: [],
  account_count: 1,
  active_account_count: 1,
  rate_limited_account_count: 0,
  model_allowlist: undefined,
  sort_order: 10
}

const AppLayoutStub = defineComponent({
  template: '<main><slot /></main>'
})

const TablePageLayoutStub = defineComponent({
  template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>'
})

const DataTableStub = defineComponent({
  props: {
    data: { type: Array, default: () => [] },
    columns: { type: Array, default: () => [] },
    loading: { type: Boolean, default: false }
  },
  template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>'
})

const BaseDialogStub = defineComponent({
  props: {
    show: { type: Boolean, default: false }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

function mountView() {
  return mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: defineComponent({
          props: ['modelValue', 'options'], emits: ['update:modelValue', 'change'],
          template: `<select :value="modelValue" @change="$emit('update:modelValue', $event.target.value); $emit('change', $event.target.value)"><option v-for="option in options" :key="String(option.value)" :value="option.value">{{ option.label }}</option></select>`
        }),
        GroupI18nField: defineComponent({
          props: ['modelValue', 'field'], emits: ['update:modelValue'],
          template: `<input :data-field="field" :value="modelValue" @input="$emit('update:modelValue', $event.target.value)" />`
        }),
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        VueDraggable: true
      }
    }
  })
}


// Owned regression for the .19 content transplant. Fixtures follow the pinned
// upstream GroupsView.duplicate pattern; assertions exercise mounted bindings.
const wrappers: ReturnType<typeof mountView>[] = []
function view() { const wrapper = mountView(); wrappers.push(wrapper); return wrapper }
function button(wrapper: ReturnType<typeof mountView>, label: string) {
  const node = wrapper.findAll('button').find(node => node.text() === label)
  expect(node, label).toBeDefined()
  return node!
}
function checkbox(wrapper: ReturnType<typeof mountView>, form: string, key: string) {
  const label = wrapper.get(form).findAll('label').find(label => label.text() === key)
  expect(label, key).toBeDefined()
  return label!.get<HTMLInputElement>('input[type="checkbox"]')
}
async function openCreate(wrapper: ReturnType<typeof mountView>, platform = 'openai') {
  await flushPromises()
  await button(wrapper, 'admin.groups.createGroup').trigger('click')
  await wrapper.get('#create-group-form input[data-field="name"]').setValue('Synthetic image group')
  await wrapper.get('#create-group-form [data-tour="group-form-platform"]').setValue(platform)
}
async function openEdit(wrapper: ReturnType<typeof mountView>) {
  await flushPromises()
  await button(wrapper, 'common.edit').trigger('click')
  await flushPromises()
}
beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  authState.isSimpleMode = false
  listGroups.mockResolvedValue({ items: [sourceGroup], total: 1, page: 1, page_size: 20, pages: 1 })
  getModelAllowlistCandidates.mockResolvedValue([])
  getUsageSummary.mockResolvedValue([])
  getCapacitySummary.mockResolvedValue([])
  getLiveCapability.mockResolvedValue({ supported: false })
  vi.mocked(adminAPI.groups.getAll).mockResolvedValue([])
  vi.mocked(adminAPI.groups.create).mockResolvedValue(sourceGroup)
  updateGroup.mockResolvedValue(sourceGroup)
  // Any accidentally unmocked HTTP is a failure, never a live fixture.
  vi.stubGlobal('fetch', vi.fn(() => { throw new Error('Unexpected network request') }))
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.unstubAllGlobals(); vi.restoreAllMocks() })
describe('custom .19 image-count billing form contract', () => {
  it('creates a default-off group without silently enabling image-count billing', async () => {
    const wrapper = view()
    await openCreate(wrapper)
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()
    expect(adminAPI.groups.create).toHaveBeenCalledWith(expect.objectContaining({ image_count_billing: false }))
  })
  it.each(['openai', 'composite'])('submits the checked image-count value for %s creation', async platform => {
    const wrapper = view()
    await openCreate(wrapper, platform)
    await checkbox(wrapper, '#create-group-form', 'admin.groups.imagePricing.allowImageGeneration').setValue(true)
    const count = checkbox(wrapper, '#create-group-form', 'admin.groups.imagePricing.perImageBilling')
    expect(count.element.checked).toBe(false)
    await count.setValue(true)
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()
    expect(adminAPI.groups.create).toHaveBeenCalledWith(expect.objectContaining({ platform, allow_image_generation: true, image_count_billing: true }))
  })
  it('hydrates existing true and submits an explicit false after unchecking', async () => {
    const wrapper = view()
    await openEdit(wrapper)
    const count = checkbox(wrapper, '#edit-group-form', 'admin.groups.imagePricing.perImageBilling')
    expect(count.element.checked).toBe(true)
    await count.setValue(false)
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({ image_count_billing: false }))
  })
  it('treats an older group response with no image-count property as disabled', async () => {
    const { image_count_billing: _omitted, ...olderGroup } = sourceGroup
    listGroups.mockResolvedValue({ items: [olderGroup], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = view()
    await openEdit(wrapper)
    expect(checkbox(wrapper, '#edit-group-form', 'admin.groups.imagePricing.perImageBilling').element.checked).toBe(false)
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({ image_count_billing: false }))
  })
  it('resets image-count billing when image generation is disabled', async () => {
    const wrapper = view()
    await openEdit(wrapper)
    await checkbox(wrapper, '#edit-group-form', 'admin.groups.imagePricing.allowImageGeneration').setValue(false)
    expect(wrapper.get('#edit-group-form').text()).not.toContain('admin.groups.imagePricing.perImageBilling')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({ allow_image_generation: false, image_count_billing: false }))
  })
  it('resets a selected creation value when switching to an incompatible platform', async () => {
    const wrapper = view()
    await openCreate(wrapper)
    await checkbox(wrapper, '#create-group-form', 'admin.groups.imagePricing.allowImageGeneration').setValue(true)
    await checkbox(wrapper, '#create-group-form', 'admin.groups.imagePricing.perImageBilling').setValue(true)
    await wrapper.get('#create-group-form [data-tour="group-form-platform"]').setValue('gemini')
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()
    expect(adminAPI.groups.create).toHaveBeenCalledWith(expect.objectContaining({ platform: 'gemini', image_count_billing: false }))
  })
})

// Deny the application's Axios transport as well as fetch. Caught component
// errors must not conceal an accidental unmocked request.
const transport = vi.hoisted(() => vi.fn(() => Promise.reject(new Error('Unexpected API transport'))))
vi.mock('@/api/client', async () => {
  const urls = await vi.importActual<typeof import('@/api/url')>('@/api/url')
  const client = { get: transport, post: transport, put: transport, patch: transport, delete: transport, request: transport }
  return { ...urls, apiClient: client, default: client }
})
beforeEach(() => { transport.mockClear() })
afterEach(() => { expect(transport).not.toHaveBeenCalled() })
