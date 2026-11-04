import { ref, watch } from 'vue'
import type { SelectOption } from '@/components/common/Select.vue'
import { intelligenceQuestionsAPI } from '@/api/admin/intelligenceQuestions'
import { buildScheduledTestModelOptions, isTextTestModel } from '@/utils/scheduledTestModels'
import type {
  IntelligenceQuestion,
  ScheduledTestFailureAction,
  ScheduledTestKind,
  ScheduledTestPickMode,
  ScheduledTestPlan,
  UpdateScheduledTestPlanRequest
} from '@/types'

export const DEFAULT_CHAR_ART_PROMPT = '不搜索网络，直接使用字符生成帅逼两个字'
export const MIN_KIND_RESULTS = 20

export interface KindFields {
  kind: ScheduledTestKind
  prompt: string
  pick_mode: ScheduledTestPickMode
  failure_action: ScheduledTestFailureAction
  show_in_catalog: boolean
  question_ids: number[]
}

interface PlanForm extends KindFields {
  model_id: string
  cron_expression: string
  max_results: string
}

export const newKindFields = (): KindFields => ({
  kind: 'probe',
  prompt: '',
  pick_mode: 'rotate',
  failure_action: 'none',
  show_in_catalog: true,
  question_ids: []
})

export const kindFieldsFromPlan = (plan: ScheduledTestPlan): KindFields => ({
  kind: plan.kind || 'probe',
  prompt: plan.prompt ?? '',
  pick_mode: plan.pick_mode || 'rotate',
  failure_action: plan.failure_action,
  show_in_catalog: Boolean(plan.show_in_catalog),
  question_ids: [...(plan.question_ids ?? [])]
})

/** Only sends the fields the plan kind uses; the backend rejects question IDs on non-quiz plans. */
export const kindFieldsRequest = (fields: KindFields): Pick<
  UpdateScheduledTestPlanRequest,
  'prompt' | 'pick_mode' | 'failure_action' | 'show_in_catalog' | 'question_ids'
> => {
  switch (fields.kind) {
    case 'char_art':
      return { prompt: fields.prompt, show_in_catalog: fields.show_in_catalog }
    case 'quiz':
      return {
        pick_mode: fields.pick_mode,
        failure_action: fields.failure_action,
        show_in_catalog: fields.show_in_catalog,
        question_ids: [...fields.question_ids]
      }
    default:
      return {}
  }
}

export const canSubmitPlan = (form: PlanForm): boolean => {
  if (!form.model_id || !form.cron_expression) return false
  if (form.kind === 'char_art' && !form.prompt.trim()) return false
  return form.kind === 'probe' || (Number(form.max_results) || 100) >= MIN_KIND_RESULTS
}

export function useScheduledTestKinds(source: {
  show: () => boolean
  accountId: () => number | null
  modelOptions: () => SelectOption[]
  newPlan: PlanForm
  editForm: PlanForm
}) {
  const questions = ref<IntelligenceQuestion[]>([])
  const mappedModels = ref<Record<string, string>>({})
  const showQuestionBank = ref(false)
  let mappingRequestId = 0

  watch(source.show, async (visible) => {
    showQuestionBank.value = false
    if (!visible) return
    try {
      questions.value = await intelligenceQuestionsAPI.list()
    } catch {
      // Keep the previous list so a failed load never prunes selected questions.
    }
  }, { immediate: true })

  watch(
    () => [source.show(), source.accountId(), source.modelOptions()] as const,
    async ([visible, accountId, options]) => {
      const requestId = ++mappingRequestId
      mappedModels.value = {}
      if (!visible || !accountId || options.length === 0) return
      try {
        const mapped = await intelligenceQuestionsAPI.mappedModels(accountId, options.map((option) => String(option.value)))
        if (requestId === mappingRequestId) mappedModels.value = mapped
      } catch {
        // Labels fall back to public model IDs.
      }
    },
    { immediate: true }
  )

  watch(
    () => source.newPlan.kind,
    (kind) => {
      const plan = source.newPlan
      if (kind === 'char_art' && !plan.prompt.trim()) plan.prompt = DEFAULT_CHAR_ART_PROMPT
      if (kind !== 'probe' && plan.model_id && !isTextTestModel(plan.model_id)) plan.model_id = ''
    }
  )

  watch(questions, (list) => {
    const existing = new Set(list.map((question) => question.id))
    for (const form of [source.newPlan, source.editForm]) {
      form.question_ids = form.question_ids.filter((id) => existing.has(id))
    }
  })

  const modelOptionsFor =(kind: ScheduledTestKind, current: string): SelectOption[] => {
    const all = buildScheduledTestModelOptions(source.modelOptions(), mappedModels.value)
    if (kind === 'probe') return all
    return all.filter((option) => option.value === current || isTextTestModel(String(option.value)))
  }

  return { questions, mappedModels, showQuestionBank, canSubmitPlan, modelOptionsFor }
}
