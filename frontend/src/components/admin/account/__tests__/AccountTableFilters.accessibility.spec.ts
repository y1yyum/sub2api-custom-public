import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import AccountTableFilters from '../AccountTableFilters.vue'
import Select from '@/components/common/Select.vue'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

enableAutoUnmount(afterEach)

const filters = {
  platform: 'openai', type: 'oauth', status: 'active',
  privacy_mode: '__unset__', group: 'ungrouped', search: 'preserved'
}

function render() {
  return mount(AccountTableFilters, {
    props: { searchQuery: 'preserved', filters },
    global: { stubs: { Teleport: true } }
  })
}

describe('expanded account filters custom accessibility contract', () => {
  it('keeps all five selectors visible with distinct meaningful accessible labels', () => {
    const wrapper = render()
    const selectors = wrapper.findAllComponents(Select)
    expect(selectors).toHaveLength(5)
    expect(selectors.every(select => select.isVisible())).toBe(true)
    expect(selectors.map(select => select.get('button').attributes('aria-label'))).toEqual([
      'admin.accounts.allPlatforms', 'admin.accounts.allTypes', 'admin.accounts.allStatus',
      'admin.accounts.allPrivacyModes', 'admin.accounts.allGroups'
    ])
    expect(wrapper.find('button[aria-controls]').exists()).toBe(false)
    expect(wrapper.emitted('update:filters')).toBeUndefined()
  })

  it('preserves unrelated filters when each expanded selector changes', () => {
    const wrapper = render()
    const keys = ['platform', 'type', 'status', 'privacy_mode', 'group']
    wrapper.findAllComponents(Select).forEach((select, index) => {
      select.vm.$emit('update:modelValue', '')
      select.vm.$emit('change')
      expect(wrapper.emitted('update:filters')?.[index]).toEqual([{ ...filters, [keys[index]]: '' }])
    })
    expect(wrapper.emitted('change')).toHaveLength(5)
    expect(wrapper.props('filters')).toEqual(filters)
  })
})
