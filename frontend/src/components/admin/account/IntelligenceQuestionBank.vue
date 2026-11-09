<template>
  <div class="space-y-4">
    <div class="flex justify-end">
      <button v-if="!form.open" type="button" class="btn btn-primary flex items-center gap-1.5 text-sm" data-test="add-question" @click="startCreate">
        <Icon name="plus" size="sm" :stroke-width="2" />
        {{ t('admin.scheduledTestKinds.bank.add') }}
      </button>
    </div>

    <div
      v-if="form.open"
      class="space-y-3 rounded-xl border border-primary-200 bg-primary-50/50 p-4 dark:border-primary-800 dark:bg-primary-900/20"
      data-test="question-form"
    >
      <div class="text-sm font-medium text-gray-700 dark:text-gray-300">
        {{ form.id ? t('admin.scheduledTestKinds.bank.edit') : t('admin.scheduledTestKinds.bank.add') }}
      </div>
      <TextArea v-model="form.prompt" :label="t('admin.scheduledTestKinds.bank.prompt')" :hint="t('admin.scheduledTestKinds.bank.imageHint')" :rows="3" required />
      <div>
        <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.scheduledTestKinds.bank.rule') }}</label>
        <div class="inline-flex rounded-lg border border-gray-200 p-0.5 dark:border-dark-600">
          <button
            v-for="rule in RULES"
            :key="rule"
            type="button"
            :data-rule="rule"
            :class="[
              'rounded-md px-3 py-1 text-xs font-medium transition-colors',
              form.answer_rule === rule ? 'bg-primary-500 text-white' : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700'
            ]"
            @click="form.answer_rule = rule"
          >
            {{ t(`admin.scheduledTestKinds.bank.rules.${rule}`) }}
          </button>
        </div>
        <p class="mt-1 text-xs text-gray-400">{{ t(`admin.scheduledTestKinds.bank.ruleHints.${form.answer_rule}`) }}</p>
      </div>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_auto]">
        <TextArea
          v-model="form.answers"
          :label="t('admin.scheduledTestKinds.bank.answers')"
          :hint="t('admin.scheduledTestKinds.bank.answersHint')"
          :rows="2"
          required
        />
        <div class="space-y-3">
          <div v-if="form.answer_rule === 'number'" class="w-32">
            <Input v-model="form.tolerance" type="number" :label="t('admin.scheduledTestKinds.bank.tolerance')" placeholder="0" />
          </div>
          <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <Toggle v-model="form.enabled" />
            {{ t('admin.scheduledTestKinds.bank.enabled') }}
          </label>
        </div>
      </div>
      <div class="space-y-2 rounded-lg border border-dashed border-gray-300 p-3 dark:border-dark-600">
        <TextArea v-model="trial.text" :placeholder="t('admin.scheduledTestKinds.bank.trialPlaceholder')" :rows="2" />
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="rounded-lg bg-gray-100 px-3 py-1 text-xs font-medium text-gray-700 hover:bg-gray-200 disabled:opacity-50 dark:bg-dark-600 dark:text-gray-300"
            :disabled="!trial.text || answers.length === 0 || trial.running"
            data-test="trial-grade"
            @click="runTrial"
          >
            {{ t('admin.scheduledTestKinds.bank.trial') }}
          </button>
          <span
            v-if="trial.passed !== null"
            :class="['text-xs font-medium', trial.passed ? 'text-green-600 dark:text-green-400' : 'text-amber-600 dark:text-amber-400']"
            data-test="trial-result"
          >
            {{ trial.passed ? t('admin.scheduledTestKinds.verdictPass') : t('admin.scheduledTestKinds.verdictFail') }}
          </span>
        </div>
      </div>
      <div class="flex justify-end gap-2">
        <button
          type="button"
          class="rounded-lg bg-gray-100 px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500"
          @click="form.open = false"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="flex items-center gap-1.5 rounded-lg bg-primary-500 px-3 py-1.5 text-sm font-medium text-white hover:bg-primary-600 disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="!form.prompt.trim() || answers.length === 0 || saving"
          data-test="save-question"
          @click="save"
        >
          <Icon v-if="saving" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
          {{ t('common.save') }}
        </button>
      </div>
    </div>

    <div v-if="loading" class="flex items-center justify-center py-8">
      <Icon name="refresh" size="md" class="animate-spin text-gray-400" :stroke-width="2" />
    </div>
    <p
      v-else-if="questions.length === 0"
      class="rounded-xl border border-dashed border-gray-300 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
    >
      {{ t('admin.scheduledTestKinds.bank.empty') }}
    </p>
    <ul v-else class="space-y-2">
      <li
        v-for="question in questions"
        :key="question.id"
        class="flex items-start gap-3 rounded-xl border border-gray-200 bg-white px-4 py-3 dark:border-dark-600 dark:bg-dark-800"
        :data-question-id="question.id"
      >
        <div class="min-w-0 flex-1 space-y-1">
          <img v-if="question.prompt_image" :src="question.prompt_image" :alt="question.prompt" class="max-h-64 max-w-full rounded border border-gray-200 bg-white object-contain object-left" data-test="question-image" />
          <template v-else>
            <p class="line-clamp-2 whitespace-pre-wrap text-sm text-gray-900 dark:text-gray-100">{{ question.prompt }}</p>
            <p class="text-xs text-amber-600 dark:text-amber-400">{{ t('admin.scheduledTestKinds.bank.imageMissing') }}</p>
          </template>
          <p class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-dark-700">{{ t(`admin.scheduledTestKinds.bank.rules.${question.answer_rule}`) }}</span>
            <span class="font-mono">{{ question.expected_answers.join(' / ') }}<template v-if="question.answer_rule === 'number' && question.tolerance > 0"> ±{{ question.tolerance }}</template></span>
            <span>{{ t('admin.scheduledTestKinds.bank.planCount') }} {{ question.plan_count }}</span>
          </p>
        </div>
        <Toggle :model-value="question.enabled" @update:model-value="(value: boolean) => setEnabled(question, value)" />
        <button
          type="button"
          class="rounded-lg p-1.5 text-gray-400 hover:bg-blue-50 hover:text-blue-500 dark:hover:bg-blue-900/20"
          :title="t('admin.scheduledTestKinds.bank.edit')"
          @click="startEdit(question)"
        >
          <Icon name="edit" size="sm" :stroke-width="2" />
        </button>
        <button
          type="button"
          class="rounded-lg p-1.5 text-gray-400 hover:bg-red-50 hover:text-red-500 dark:hover:bg-red-900/20"
          :title="t('common.delete')"
          @click="deleting = question"
        >
          <Icon name="trash" size="sm" :stroke-width="2" />
        </button>
      </li>
    </ul>
  </div>

  <ConfirmDialog
    :show="deleting !== null"
    :title="t('common.delete')"
    :message="t('admin.scheduledTestKinds.bank.deleteConfirm')"
    :confirm-text="t('common.delete')"
    :cancel-text="t('common.cancel')"
    :danger="true"
    @confirm="confirmDelete"
    @cancel="deleting = null"
  />
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { renderIntelligenceQuestionImage } from '@/utils/intelligenceQuestionImage'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Input from '@/components/common/Input.vue'
import TextArea from '@/components/common/TextArea.vue'
import Toggle from '@/components/common/Toggle.vue'
import { Icon } from '@/components/icons'
import { intelligenceQuestionsAPI } from '@/api/admin/intelligenceQuestions'
import { useAppStore } from '@/stores/app'
import type { IntelligenceAnswerRule, IntelligenceQuestion, IntelligenceQuestionRequest } from '@/types'

const RULES: IntelligenceAnswerRule[] = ['exact', 'number', 'regex']

const emit = defineEmits<{
  (e: 'changed', questions: IntelligenceQuestion[]): void
}>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const saving = ref(false)
const questions = ref<IntelligenceQuestion[]>([])
const deleting = ref<IntelligenceQuestion | null>(null)
const form = reactive({
  open: false,
  id: 0,
  prompt: '',
  answer_rule: 'exact' as IntelligenceAnswerRule,
  answers: '',
  tolerance: '0',
  enabled: true
})
const trial = reactive({ text: '', passed: null as boolean | null, running: false })

const answers = computed(() => form.answers.split('\n').map((line) => line.trim()).filter(Boolean))

const toRequest = (): Omit<IntelligenceQuestionRequest, 'prompt_image'> => ({
  prompt: form.prompt.trim(),
  answer_rule: form.answer_rule,
  expected_answers: answers.value,
  tolerance: form.answer_rule === 'number' ? Number(form.tolerance) || 0 : 0,
  enabled: form.enabled
})

const questionRequest = (question: IntelligenceQuestion, enabled: boolean): Omit<IntelligenceQuestionRequest, 'prompt_image'> => ({
  prompt: question.prompt,
  answer_rule: question.answer_rule,
  expected_answers: question.expected_answers,
  tolerance: question.tolerance,
  enabled
})

const publish = () => emit('changed', questions.value)

const load = async () => {
  loading.value = true
  try {
    questions.value = await intelligenceQuestionsAPI.list()
    publish()
  } catch (error: any) {
    appStore.showError(error?.message || 'Failed to load questions')
  } finally {
    loading.value = false
  }
}

onMounted(load)

watch(
  () => [form.answer_rule, form.answers, form.tolerance],
  () => { trial.passed = null }
)

const resetTrial = () => {
  trial.text = ''
  trial.passed = null
}

const startCreate = () => {
  Object.assign(form, { open: true, id: 0, prompt: '', answer_rule: 'exact', answers: '', tolerance: '0', enabled: true })
  resetTrial()
}

const startEdit = (question: IntelligenceQuestion) => {
  Object.assign(form, {
    open: true,
    id: question.id,
    prompt: question.prompt,
    answer_rule: question.answer_rule,
    answers: question.expected_answers.join('\n'),
    tolerance: String(question.tolerance),
    enabled: question.enabled
  })
  resetTrial()
}

const replaceQuestion = (saved: IntelligenceQuestion) => {
  const index = questions.value.findIndex((q) => q.id === saved.id)
  if (index === -1) questions.value = [...questions.value, saved]
  else questions.value = questions.value.map((q) => (q.id === saved.id ? saved : q))
  publish()
}

const save = async () => {
  saving.value = true
  try {
    const request = { ...toRequest(), prompt_image: await renderIntelligenceQuestionImage(form.prompt) }
    const saved = form.id
      ? await intelligenceQuestionsAPI.update(form.id, request)
      : await intelligenceQuestionsAPI.create(request)
    replaceQuestion(saved)
    form.open = false
    appStore.showSuccess(t('admin.scheduledTestKinds.bank.saved'))
  } catch (error: any) {
    appStore.showError(error?.message || 'Failed to save question')
  } finally {
    saving.value = false
  }
}

const setEnabled = async (question: IntelligenceQuestion, enabled: boolean) => {
  try {
    replaceQuestion(await intelligenceQuestionsAPI.update(question.id, {
      ...questionRequest(question, enabled),
      prompt_image: question.prompt_image || await renderIntelligenceQuestionImage(question.prompt)
    }))
  } catch (error: any) {
    appStore.showError(error?.message || 'Failed to update question')
  }
}

const confirmDelete = async () => {
  const target = deleting.value
  deleting.value = null
  if (!target) return
  try {
    await intelligenceQuestionsAPI.remove(target.id)
    questions.value = questions.value.filter((q) => q.id !== target.id)
    publish()
    appStore.showSuccess(t('admin.scheduledTestKinds.bank.deleted'))
  } catch (error: any) {
    appStore.showError(error?.message || 'Failed to delete question')
  }
}

const runTrial = async () => {
  trial.running = true
  try {
    trial.passed = await intelligenceQuestionsAPI.grade({
      answer_rule: form.answer_rule,
      expected_answers: answers.value,
      tolerance: toRequest().tolerance,
      response_text: trial.text
    })
  } catch (error: any) {
    trial.passed = null
    appStore.showError(error?.message || 'Failed to grade')
  } finally {
    trial.running = false
  }
}
</script>
