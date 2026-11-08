import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh/admin/resources'
import en from '@/i18n/locales/en/admin/resources'
import UsageDiagnostics from '../UsageDiagnostics.vue'
import type { UsageResponseDiagnostics } from '@/types'

const sample: UsageResponseDiagnostics = {
  protocol: 'anthropic', display_scaled: true, input_change_rate: 0.154599,
  initial_usage: { input_tokens: 4, cache_read_tokens: 1412896, cache_creation_tokens: 258377, total_input_tokens: 1671277, output_tokens: 0, display_scaled: true },
  final_usage: { input_tokens: 4, cache_read_tokens: 1631328, cache_creation_tokens: 298323, total_input_tokens: 1929655, output_tokens: 6902, display_scaled: true },
  stop_reason: 'tool_use', stream_complete: true, event_count: 6, thinking_tokens: 3459,
  message_id: 'msg_example', service_tier: 'standard', inference_geo: 'not_available',
  tool_calls: [{ type: 'tool_use', name: 'editor', count: 1 }],
}

function render(diagnostics?: UsageResponseDiagnostics, locale = 'zh') {
  return mount(UsageDiagnostics, {
    props: { diagnostics },
    attachTo: document.body,
    global: { plugins: [createI18n({ legacy: false, locale, messages: { zh: { admin: zh }, en: { admin: en } } })] },
  })
}

afterEach(() => { document.body.innerHTML = ''; document.body.classList.remove('modal-open') })

describe('administrator usage diagnostics', () => {
  it('uses compact badges and opens upstream initial/final declarations in a dialog', async () => {
    const wrapper = render(sample)
    expect(wrapper.text()).toContain('已缩放')
    expect(wrapper.text()).toContain('+15.46%')
    expect(wrapper.text()).toContain('工具调用')
    expect(wrapper.find('[aria-label="已观测到完整结束"]').exists()).toBe(true)
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    await wrapper.get('button').trigger('click')
    const dialog = document.querySelector('[role="dialog"]')!
    expect(dialog.textContent).toContain('1,412,896')
    expect(dialog.textContent).toContain('1,631,328')
    expect(dialog.textContent).toContain('3,459')
    expect(dialog.textContent).toContain('不能当作独立测量的真实消耗')
    expect(dialog.textContent).toContain('editor')
    expect(dialog.textContent).not.toContain('not_available')
    wrapper.unmount()
  })

  it('keeps absent values unknown and preserves a declared zero', async () => {
    const wrapper = render({ protocol: 'anthropic', stream_complete: false, event_count: 1, initial_usage: { input_tokens: 0 }, error_type: 'overloaded_error' })
    expect(wrapper.find('[aria-label="未完整结束或流内错误"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('已缩放')
    await wrapper.get('button').trigger('click')
    const dialog = document.querySelector('[role="dialog"]')!
    const cells = [...dialog.querySelectorAll('tbody tr:first-child td')]
    expect(cells[1]?.textContent).toBe('0')
    expect(cells[2]?.textContent).toBe('未返回')
    expect(dialog.textContent).toContain('overloaded_error')
    wrapper.unmount()
  })

  it('does not invent diagnostics for historical records', async () => {
    const wrapper = render()
    expect(wrapper.find('[aria-label="已观测到完整结束"]').exists()).toBe(false)
    await wrapper.get('button').trigger('click')
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('此记录没有响应诊断数据')
    wrapper.unmount()
  })

  it('provides localized English badges and details', async () => {
    const wrapper = render(sample, 'en')
    expect(wrapper.text()).toContain('Scaled')
    expect(wrapper.text()).toContain('Tool call')
    await wrapper.get('button').trigger('click')
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Initial declaration')
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Final declaration')
    wrapper.unmount()
  })
})
