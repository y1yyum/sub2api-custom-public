<template>
  <section v-if="visible" :aria-labelledby="headingId" data-testid="detail-intelligence">
    <h3 :id="headingId" class="mb-2 flex items-baseline justify-between gap-3 text-sm font-semibold text-gray-900 dark:text-white">
      {{ t('modelChannelStatus.detail.intelligence.title') }}
      <span v-if="latestModel" class="truncate font-mono text-xs font-normal text-gray-500 dark:text-dark-400" data-testid="intelligence-model">
        {{ latestModel }}
      </span>
    </h3>
    <p v-if="!data" class="text-sm text-amber-700 dark:text-amber-300" data-testid="intelligence-unavailable">
      {{ t('modelChannelStatus.detail.intelligence.unavailable') }}
    </p>
    <template v-else>
      <div v-if="data.quiz.items.length > 0" class="flex items-center gap-3" data-testid="intelligence-quiz">
        <span class="w-12 shrink-0 text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.intelligence.quiz') }}</span>
        <div class="flex h-5 flex-1 items-stretch gap-px" role="group" :aria-label="t('modelChannelStatus.detail.intelligence.quiz')">
          <button
            v-for="(item, index) in data.quiz.items"
            :key="index"
            type="button"
            class="min-w-[6px] flex-1 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary-500"
            :class="[quizBlockClass(item.outcome), isSelected('quiz', index) ? 'outline outline-2 outline-offset-1 outline-gray-900 dark:outline-white' : '']"
            :aria-label="itemLabel(item.checked_at, t(`modelChannelStatus.detail.intelligence.outcome.${item.outcome}`))"
            :aria-pressed="isSelected('quiz', index)"
            data-testid="quiz-block"
            :data-outcome="item.outcome"
            @click="select('quiz', index)"
          />
        </div>
        <span
          class="w-12 shrink-0 text-right text-xs font-medium tabular-nums text-gray-900 dark:text-white"
          :title="t('modelChannelStatus.detail.intelligence.scoreTitle', { passed: data.quiz.passed, scored: data.quiz.scored })"
          data-testid="quiz-score"
        >
          {{ data.quiz.passed }}/{{ data.quiz.scored }}
        </span>
      </div>

      <div v-if="data.char_art.length > 0" class="mt-2 flex items-center gap-3" data-testid="intelligence-char-art">
        <span class="w-12 shrink-0 text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.intelligence.charArt') }}</span>
        <div
          ref="charScroller"
          class="flex flex-1 gap-1 overflow-x-auto pb-1"
          role="group"
          :aria-label="t('modelChannelStatus.detail.intelligence.charArt')"
        >
          <button
            v-for="(item, index) in data.char_art"
            :key="index"
            type="button"
            class="shrink-0 border px-1.5 py-0.5 text-[11px] tabular-nums transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500"
            :class="charButtonClass(item.outcome, isSelected('charArt', index))"
            :aria-label="itemLabel(item.checked_at, t(`modelChannelStatus.detail.intelligence.outcome.${item.outcome}`))"
            :aria-pressed="isSelected('charArt', index)"
            data-testid="char-art-button"
            :data-outcome="item.outcome"
            @click="select('charArt', index)"
          >
            {{ formatCheckTime(item.checked_at, withDate) }}
          </button>
        </div>
      </div>

      <div
        v-if="current"
        class="mt-3 border border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900/40"
        data-testid="intelligence-content"
      >
        <p class="mb-1.5 flex flex-wrap gap-x-2 text-[11px] tabular-nums text-gray-500 dark:text-dark-400">
          <time :datetime="current.item.checked_at">{{ formatCheckTime(current.item.checked_at, true) }}</time>
          <span class="font-mono" data-testid="intelligence-content-model">{{ current.item.model }}</span>
        </p>
        <pre
          v-if="current.kind === 'charArt'"
          class="m-0 max-h-72 overflow-auto whitespace-pre font-mono text-xs leading-tight text-gray-900 dark:text-white"
          data-testid="char-art-text"
        >{{ current.item.outcome === 'success' ? current.item.response_text : '—' }}</pre>
        <dl v-else class="m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs" data-testid="quiz-detail">
          <dt class="text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.intelligence.question') }}</dt>
          <dd class="m-0 whitespace-pre-wrap break-words text-gray-900 dark:text-white">{{ current.item.prompt }}</dd>
          <dt class="text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.intelligence.answer') }}</dt>
          <dd class="m-0 whitespace-pre-wrap break-words" :class="answerClass(current.item.outcome)" data-testid="quiz-answer">
            {{ current.item.outcome === 'error' ? '—' : current.item.response_text }}
          </dd>
          <dt class="text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.intelligence.expected') }}</dt>
          <dd class="m-0 whitespace-pre-wrap break-words text-gray-900 dark:text-white" data-testid="quiz-expected">{{ current.item.expected_answer }}</dd>
        </dl>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  CatalogBlock,
  CatalogCharArtItem,
  CatalogIntelligence,
  CatalogQuizItem,
  CatalogQuizOutcome,
} from '@/api/channelStatusCatalog'
import { formatCheckTime, spansDays } from './catalogFormat'

type Kind = 'quiz' | 'charArt'
type Current = { kind: 'quiz'; item: CatalogQuizItem } | { kind: 'charArt'; item: CatalogCharArtItem }

const props = defineProps<{ block: CatalogBlock<CatalogIntelligence> }>()

const { t } = useI18n()
const headingId = 'catalog-detail-intelligence-heading'

const data = computed(() => props.block.data)
// No plan shows checks for this row: hide the section rather than say so.
const visible = computed(() => {
  const d = data.value
  if (!d) return props.block.error_code != null && props.block.error_code !== 'no_data'
  return d.quiz.items.length > 0 || d.char_art.length > 0
})

const withDate = computed(() => spansDays(data.value?.char_art.map((item) => item.checked_at) ?? []))

const latestModel = computed(() => {
  const d = data.value
  if (!d) return ''
  const items: Array<CatalogQuizItem | CatalogCharArtItem> = [...d.quiz.items, ...d.char_art]
  let latest: CatalogQuizItem | CatalogCharArtItem | undefined
  for (const item of items) {
    if (!latest || Date.parse(item.checked_at) >= Date.parse(latest.checked_at)) latest = item
  }
  return latest?.model ?? ''
})

const selection = ref<{ kind: Kind; index: number } | null>(null)
const charScroller = ref<HTMLElement | null>(null)

const current = computed<Current | null>(() => {
  const d = data.value
  const s = selection.value
  if (!d || !s) return null
  if (s.kind === 'quiz') {
    const item = d.quiz.items[s.index]
    return item ? { kind: 'quiz', item } : null
  }
  const item = d.char_art[s.index]
  return item ? { kind: 'charArt', item } : null
})

// Default to the newest char art reply, else the newest quiz run, and scroll
// the time buttons to the newest end.
watch(
  data,
  async (d) => {
    if (!d) {
      selection.value = null
      return
    }
    if (d.char_art.length > 0) selection.value = { kind: 'charArt', index: d.char_art.length - 1 }
    else if (d.quiz.items.length > 0) selection.value = { kind: 'quiz', index: d.quiz.items.length - 1 }
    else selection.value = null
    await nextTick()
    if (charScroller.value) charScroller.value.scrollLeft = charScroller.value.scrollWidth
  },
  { immediate: true },
)

function select(kind: Kind, index: number) {
  selection.value = { kind, index }
}

function isSelected(kind: Kind, index: number): boolean {
  return selection.value?.kind === kind && selection.value.index === index
}

function itemLabel(iso: string, outcome: string): string {
  return `${formatCheckTime(iso, true)} ${outcome}`
}

function quizBlockClass(outcome: CatalogQuizOutcome): string {
  switch (outcome) {
    case 'pass':
      return 'bg-emerald-500 dark:bg-emerald-400'
    case 'fail':
      return 'bg-red-500 dark:bg-red-400'
    default:
      return 'bg-gray-300 dark:bg-dark-500'
  }
}

function charButtonClass(outcome: CatalogCharArtItem['outcome'], selected: boolean): string {
  if (outcome === 'error') {
    return selected
      ? 'border-dashed border-red-500 bg-red-50 text-red-700 dark:bg-red-950/40 dark:text-red-300'
      : 'border-dashed border-red-400 text-red-600 hover:bg-red-50 dark:text-red-300 dark:hover:bg-red-950/30'
  }
  return selected
    ? 'border-gray-900 bg-gray-900 text-white dark:border-white dark:bg-white dark:text-dark-900'
    : 'border-gray-300 text-gray-700 hover:bg-gray-100 dark:border-dark-600 dark:text-dark-200 dark:hover:bg-dark-700'
}

function answerClass(outcome: CatalogQuizOutcome): string {
  if (outcome === 'pass') return 'text-emerald-600 dark:text-emerald-400'
  if (outcome === 'fail') return 'text-red-600 dark:text-red-400'
  return 'text-gray-500 dark:text-dark-400'
}
</script>
