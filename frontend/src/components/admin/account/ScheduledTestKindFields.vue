<template>
  <div class="space-y-3">
    <div>
      <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
        {{ t('admin.scheduledTestKinds.type') }}
      </label>
      <div v-if="kindEditable" class="inline-flex rounded-lg border border-gray-200 p-0.5 dark:border-dark-600" role="radiogroup">
        <button
          v-for="option in KIND_OPTIONS"
          :key="option"
          type="button"
          role="radio"
          :aria-checked="kind === option"
          :data-kind="option"
          :class="[
            'rounded-md px-3 py-1 text-xs font-medium transition-colors',
            kind === option
              ? 'bg-primary-500 text-white'
              : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700'
          ]"
          @click="kind = option"
        >
          {{ t(`admin.scheduledTestKinds.kinds.${option}`) }}
        </button>
      </div>
      <span v-else class="inline-flex rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-300">
        {{ t(`admin.scheduledTestKinds.kinds.${kind}`) }}
      </span>
    </div>

    <TextArea
      v-if="kind === 'char_art'"
      v-model="prompt"
      :label="t('admin.scheduledTestKinds.prompt')"
      :rows="3"
      required
    />

    <div v-if="kind === 'quiz'" class="space-y-2">
      <div class="flex items-center justify-between">
        <span class="text-xs font-medium text-gray-600 dark:text-gray-400">
          {{ t('admin.scheduledTestKinds.questions') }}
          <span class="ml-1 text-gray-400">{{ t('admin.scheduledTestKinds.questionsSelected', { count: questionIds.length }) }}</span>
        </span>
        <button type="button" class="text-xs font-medium text-primary-600 hover:underline dark:text-primary-400" @click="emit('open-bank')">
          {{ t('admin.scheduledTestKinds.openBank') }}
        </button>
      </div>
      <p v-if="questions.length === 0" class="rounded-lg border border-dashed border-gray-300 px-3 py-2 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">
        {{ t('admin.scheduledTestKinds.noQuestions') }}
      </p>
      <div v-else class="max-h-40 space-y-1 overflow-y-auto rounded-lg border border-gray-200 p-2 dark:border-dark-600">
        <label
          v-for="question in questions"
          :key="question.id"
          class="flex cursor-pointer items-start gap-2 rounded px-1 py-0.5 text-xs hover:bg-gray-50 dark:hover:bg-dark-700"
        >
          <input
            type="checkbox"
            class="mt-0.5 h-3.5 w-3.5 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
            :checked="questionIds.includes(question.id)"
            :data-question-id="question.id"
            @change="toggleQuestion(question.id)"
          />
          <span class="min-w-0 flex-1">
            <span class="line-clamp-1 text-gray-800 dark:text-gray-200">{{ question.prompt }}</span>
            <span class="text-gray-400">{{ question.expected_answers.join(' / ') }}</span>
            <span v-if="!question.enabled" class="ml-1 text-amber-600">{{ t('admin.scheduledTestKinds.disabledQuestion') }}</span>
          </span>
        </label>
      </div>
      <div class="flex items-center gap-2">
        <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.scheduledTestKinds.pickMode') }}</span>
        <div class="inline-flex rounded-lg border border-gray-200 p-0.5 dark:border-dark-600">
          <button
            v-for="mode in PICK_MODES"
            :key="mode"
            type="button"
            :class="[
              'rounded-md px-2 py-0.5 text-xs transition-colors',
              pickMode === mode ? 'bg-gray-200 font-medium text-gray-900 dark:bg-dark-600 dark:text-white' : 'text-gray-500'
            ]"
            @click="pickMode = mode"
          >
            {{ mode === 'rotate' ? t('admin.scheduledTestKinds.pickRotate') : t('admin.scheduledTestKinds.pickRandom') }}
          </button>
        </div>
      </div>
    </div>

    <label v-if="kind !== 'probe'" class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
      <Toggle v-model="showInCatalog" />
      {{ t('admin.scheduledTestKinds.showInCatalog') }}
    </label>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import TextArea from '@/components/common/TextArea.vue'
import Toggle from '@/components/common/Toggle.vue'
import type { IntelligenceQuestion, ScheduledTestKind, ScheduledTestPickMode } from '@/types'

const KIND_OPTIONS: ScheduledTestKind[] = ['probe', 'char_art', 'quiz']
const PICK_MODES: ScheduledTestPickMode[] = ['rotate', 'random']

defineProps<{
  kindEditable: boolean
  questions: IntelligenceQuestion[]
}>()

const emit = defineEmits<{
  (e: 'open-bank'): void
}>()

const kind = defineModel<ScheduledTestKind>('kind', { required: true })
const prompt = defineModel<string>('prompt', { required: true })
const pickMode = defineModel<ScheduledTestPickMode>('pickMode', { required: true })
const showInCatalog = defineModel<boolean>('showInCatalog', { required: true })
const questionIds = defineModel<number[]>('questionIds', { required: true })

const { t } = useI18n()

const toggleQuestion = (id: number) => {
  questionIds.value = questionIds.value.includes(id)
    ? questionIds.value.filter((existing) => existing !== id)
    : [...questionIds.value, id]
}
</script>
