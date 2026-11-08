import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))
enableAutoUnmount(afterEach)

const api = vi.hoisted(() => ({
  list: vi.fn(), create: vi.fn(), update: vi.fn(), remove: vi.fn(), grade: vi.fn(),
  showError: vi.fn(), showSuccess: vi.fn(),
}))
vi.mock('@/utils/intelligenceQuestionImage', () => ({
  renderIntelligenceQuestionImage: vi.fn(async () => 'data:image/png;base64,question'),
}))
vi.mock('@/api/admin/intelligenceQuestions', () => ({
  intelligenceQuestionsAPI: { list: api.list, create: api.create, update: api.update, remove: api.remove, grade: api.grade },
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: api.showError, showSuccess: api.showSuccess }) }))

import IntelligenceQuestionBankModal from '../IntelligenceQuestionBankModal.vue'
import { renderIntelligenceQuestionImage } from '@/utils/intelligenceQuestionImage'

const question = {
  id: 3, prompt: '6*7=?', answer_rule: 'number', expected_answers: ['42'], tolerance: 0.5, enabled: true, plan_count: 2,
  created_at: '', updated_at: '',
}

async function openModal(): Promise<VueWrapper> {
  const wrapper = mount(IntelligenceQuestionBankModal, {
    props: { show: true },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /></div>' },
        ConfirmDialog: { props: ['show'], emits: ['confirm'], template: '<button v-if="show" data-test="confirm" @click="$emit(\'confirm\')" />' },
      },
    },
  })
  await flushPromises()
  return wrapper
}

describe('intelligence question bank', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(renderIntelligenceQuestionImage).mockResolvedValue('data:image/png;base64,question')
    api.list.mockResolvedValue([question])
  })

  it('lists questions with rule, answers and plan count', async () => {
    const wrapper = await openModal()
    const row = wrapper.find('[data-question-id="3"]')
    expect(row.text()).toContain('6*7=?')
    expect(row.text()).toContain('42 ±0.5')
    expect(row.text()).toContain('admin.scheduledTestKinds.bank.planCount 2')
    expect(wrapper.emitted('changed')?.[0]).toEqual([[question]])
  })

  it('creates a question with one answer per line', async () => {
    api.create.mockResolvedValue({ ...question, id: 4, prompt: 'capital?', answer_rule: 'exact', expected_answers: ['Paris', '巴黎'], tolerance: 0 })
    const wrapper = await openModal()
    await wrapper.find('[data-test="add-question"]').trigger('click')
    const [prompt, answers] = wrapper.findAll('[data-test="question-form"] textarea')
    await prompt.setValue('capital?')
    await answers.setValue('Paris\n\n 巴黎 \n')
    await wrapper.find('[data-test="save-question"]').trigger('click')
    await flushPromises()
    expect(api.create).toHaveBeenCalledWith({
      prompt: 'capital?', prompt_image: 'data:image/png;base64,question', answer_rule: 'exact', expected_answers: ['Paris', '巴黎'], tolerance: 0, enabled: true,
    })
    expect(wrapper.findAll('li')).toHaveLength(2)
    expect(renderIntelligenceQuestionImage).toHaveBeenCalledWith('capital?')
  })

  it('previews grading with the form answers', async () => {
    api.grade.mockResolvedValue(true)
    const wrapper = await openModal()
    await wrapper.find('[title="admin.scheduledTestKinds.bank.edit"]').trigger('click')
    const textareas = wrapper.findAll('[data-test="question-form"] textarea')
    await textareas[2].setValue('答案：４２')
    await wrapper.find('[data-test="trial-grade"]').trigger('click')
    await flushPromises()
    expect(api.grade).toHaveBeenCalledWith({ answer_rule: 'number', expected_answers: ['42'], tolerance: 0.5, response_text: '答案：４２' })
    expect(wrapper.find('[data-test="trial-result"]').text()).toBe('admin.scheduledTestKinds.verdictPass')
  })

  it('toggles enabled with the full question and deletes after confirmation', async () => {
    api.update.mockResolvedValue({ ...question, enabled: false })
    api.remove.mockResolvedValue(undefined)
    const wrapper = await openModal()
    await wrapper.find('[role="switch"]').trigger('click')
    await flushPromises()
    expect(api.update).toHaveBeenCalledWith(3, {
      prompt: '6*7=?', prompt_image: 'data:image/png;base64,question', answer_rule: 'number', expected_answers: ['42'], tolerance: 0.5, enabled: false,
    })

    await wrapper.find('[title="common.delete"]').trigger('click')
    await wrapper.find('[data-test="confirm"]').trigger('click')
    await flushPromises()
    expect(api.remove).toHaveBeenCalledWith(3)
    expect(wrapper.findAll('li')).toHaveLength(0)
    expect(wrapper.emitted('changed')?.at(-1)).toEqual([[]])
  })

  it('shows the saved image and reuses it when toggling enabled', async () => {
    const saved = { ...question, prompt_image: 'data:image/png;base64,saved' }
    api.list.mockResolvedValue([saved])
    api.update.mockResolvedValue({ ...saved, enabled: false })
    const wrapper = await openModal()
    expect(wrapper.get('[data-test="question-image"]').attributes('src')).toBe(saved.prompt_image)
    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()
    expect(renderIntelligenceQuestionImage).not.toHaveBeenCalled()
    expect(api.update).toHaveBeenCalledWith(3, expect.objectContaining({ prompt_image: saved.prompt_image }))
  })

  it('regenerates the image after editing the question', async () => {
    api.update.mockResolvedValue({ ...question, prompt: 'new question', prompt_image: 'data:image/png;base64,question' })
    const wrapper = await openModal()
    await wrapper.get('[title="admin.scheduledTestKinds.bank.edit"]').trigger('click')
    await wrapper.findAll('[data-test="question-form"] textarea')[0].setValue('new question')
    await wrapper.get('[data-test="save-question"]').trigger('click')
    await flushPromises()
    expect(renderIntelligenceQuestionImage).toHaveBeenCalledWith('new question')
    expect(api.update).toHaveBeenCalledWith(3, expect.objectContaining({ prompt: 'new question', prompt_image: 'data:image/png;base64,question' }))
  })

  it('keeps the form open and does not save when image generation fails', async () => {
    vi.mocked(renderIntelligenceQuestionImage).mockRejectedValueOnce(new Error('canvas failed'))
    const wrapper = await openModal()
    await wrapper.get('[title="admin.scheduledTestKinds.bank.edit"]').trigger('click')
    await wrapper.get('[data-test="save-question"]').trigger('click')
    await flushPromises()
    expect(api.update).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="question-form"]').exists()).toBe(true)
    expect(api.showError).toHaveBeenCalledWith('canvas failed')
  })
})
