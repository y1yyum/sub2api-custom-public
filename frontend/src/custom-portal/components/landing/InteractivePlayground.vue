<template>
  <section id="playground" class="relative py-20 lg:py-28">
    <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
      <!-- Section Header -->
      <div class="mx-auto max-w-3xl text-center">
        <div class="inline-flex items-center gap-1.5 rounded-full bg-primary-100/80 px-3 py-1 text-xs font-semibold text-primary-700 dark:bg-primary-950/60 dark:text-primary-300">
          <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 13.5l10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z" />
          </svg>
          {{ pt('playground.badge') }}
        </div>
        <h2 class="mt-3 text-3xl font-bold tracking-tight text-gray-900 sm:text-4xl dark:text-white">
          {{ pt('playground.title') }}
        </h2>
        <p class="mt-3 text-base text-gray-600 dark:text-dark-300">
          {{ pt('playground.description') }}
        </p>
      </div>

      <!-- Playground Shell -->
      <div class="mt-12 overflow-hidden rounded-2xl border border-gray-200/80 bg-white/80 shadow-2xl backdrop-blur-xl dark:border-dark-700 dark:bg-dark-900/80">
        <!-- Control Header -->
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200/60 bg-gray-50/70 px-6 py-4 dark:border-dark-800 dark:bg-dark-950/60">
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ pt('playground.exampleModel') }}</span>
            <div class="flex flex-wrap gap-1.5">
              <button
                v-for="m in modelOptions"
                :key="m.id"
                @click="selectedModel = m.id"
                class="rounded-lg px-3 py-1 text-xs font-medium transition-all"
                :class="selectedModel === m.id ? 'bg-primary-600 text-white shadow-sm' : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-800 dark:text-dark-300 dark:hover:bg-dark-700'"
              >
                {{ m.name }}
              </button>
            </div>
          </div>

          <!-- Mode / Protocol Pill -->
          <div class="flex items-center gap-2 text-xs">
            <span class="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 font-mono text-[11px] font-semibold text-emerald-600 dark:text-emerald-400">
              {{ pt('playground.protocol', { protocol: currentProtocol }) }}
            </span>
            <span class="rounded-md border border-blue-500/30 bg-blue-500/10 px-2 py-0.5 font-mono text-[11px] font-semibold text-blue-600 dark:text-blue-400">
              {{ pt('playground.sse') }}
            </span>
          </div>
        </div>

        <!-- Main Workspace -->
        <div class="grid grid-cols-1 lg:grid-cols-12">
          <!-- Left: Prompt Selection & Input (5 cols) -->
          <div class="flex flex-col border-b border-gray-200/60 p-6 lg:col-span-5 lg:border-b-0 lg:border-r dark:border-dark-800">
            <div class="mb-3 flex items-center justify-between">
              <span class="text-xs font-bold text-gray-700 dark:text-gray-300">{{ pt('playground.presets') }}</span>
              <span class="text-[11px] text-gray-400">{{ pt('playground.presetsHint') }}</span>
            </div>
            <div class="space-y-2">
              <button
                v-for="p in promptPresets"
                :key="p.id"
                @click="activePrompt = p.text"
                class="w-full text-left rounded-xl border border-gray-200/60 bg-gray-50/60 p-3 text-xs transition-all hover:border-primary-500/40 hover:bg-white dark:border-dark-800 dark:bg-dark-950/40 dark:hover:border-primary-500/30 dark:hover:bg-dark-800/80"
                :class="activePrompt === p.text ? 'border-primary-500/60 ring-1 ring-primary-500/20' : ''"
              >
                <div class="font-medium text-gray-800 dark:text-gray-200">{{ p.title }}</div>
                <div class="mt-1 line-clamp-1 text-[11px] text-gray-500 dark:text-dark-400">{{ p.text }}</div>
              </button>
            </div>

            <!-- Custom Input -->
            <div class="mt-4 flex flex-1 flex-col">
              <label class="mb-1 text-xs font-bold text-gray-700 dark:text-gray-300">{{ pt('playground.customPrompt') }}</label>
              <textarea
                v-model="activePrompt"
                rows="4"
                class="w-full resize-none rounded-xl border border-gray-200 bg-white p-3 text-xs text-gray-900 shadow-sm transition-all focus:border-primary-500 focus:outline-none focus:ring-1 focus:ring-primary-500 dark:border-dark-700 dark:bg-dark-950 dark:text-white"
                :placeholder="pt('playground.customPromptPlaceholder')"
              ></textarea>
            </div>

            <!-- Send Action Button -->
            <div class="mt-5 flex items-center gap-3">
              <button
                @click="runSimulation"
                :disabled="isStreaming"
                class="flex flex-1 items-center justify-center gap-2 rounded-xl bg-gradient-to-r from-primary-600 to-primary-500 px-5 py-2.5 text-xs font-bold text-white shadow-md shadow-primary-500/20 transition-all hover:from-primary-500 hover:to-primary-600 hover:shadow-lg hover:shadow-primary-500/30 active:scale-95 disabled:cursor-not-allowed disabled:opacity-60"
              >
                <svg v-if="isStreaming" class="h-4 w-4 animate-spin text-white" fill="none" viewBox="0 0 24 24">
                  <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                  <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z"></path>
                </svg>
                <svg v-else class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 12L3.269 3.126A59.768 59.768 0 0121.485 12 59.77 59.77 0 013.27 20.876L5.999 12zm0 0h7.5" />
                </svg>
                <span>{{ isStreaming ? pt('playground.playing') : pt('playground.play') }}</span>
              </button>

              <button
                @click="resetStream"
                type="button"
                class="rounded-xl border border-gray-200 px-3.5 py-2.5 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:border-dark-700 dark:text-dark-300 dark:hover:bg-dark-800"
              >
                {{ pt('playground.clear') }}
              </button>
            </div>
          </div>

          <!-- Right: Terminal & Stream Output (7 cols) -->
          <div class="flex flex-col bg-gray-950 font-mono text-xs text-gray-100 lg:col-span-7">
            <!-- Terminal Topbar -->
            <div class="flex items-center justify-between border-b border-gray-800 bg-gray-900/90 px-4 py-2.5 text-gray-400">
              <div class="flex items-center gap-2">
                <span class="flex gap-1.5">
                  <span class="h-2.5 w-2.5 rounded-full bg-red-500/80"></span>
                  <span class="h-2.5 w-2.5 rounded-full bg-amber-500/80"></span>
                  <span class="h-2.5 w-2.5 rounded-full bg-emerald-500/80"></span>
                </span>
                <span class="text-[11px] text-gray-400">{{ pt('playground.terminalTitle') }}</span>
              </div>
              <span class="rounded bg-amber-950 px-1.5 py-0.5 text-[11px] font-semibold text-amber-300">{{ pt('playground.terminalBadge') }}</span>
            </div>

            <!-- Lifecycle Trace Steps -->
            <div class="border-b border-gray-800/80 bg-gray-900/40 px-4 py-2 text-[11px] text-gray-400">
              <div class="flex flex-wrap items-center gap-4">
                <div class="flex items-center gap-1.5" :class="step >= 1 ? 'text-emerald-400' : 'text-gray-500'">
                  <span>{{ pt('playground.stepAuth') }}</span>
                  <svg class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" /></svg>
                </div>
                <div class="flex items-center gap-1.5" :class="step >= 2 ? 'text-emerald-400' : 'text-gray-500'">
                  <span>{{ pt('playground.stepRoute') }}</span>
                  <svg class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" /></svg>
                </div>
                <div class="flex items-center gap-1.5" :class="step >= 3 ? 'text-emerald-400' : 'text-gray-500'">
                  <span>{{ pt('playground.stepStream') }}</span>
                  <svg class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" /></svg>
                </div>
              </div>
            </div>

            <!-- Terminal Output Stream -->
            <div class="min-h-[280px] max-h-[340px] flex-1 overflow-y-auto p-4 leading-relaxed whitespace-pre-wrap select-text">
              <p v-if="streamedOutput" class="text-gray-100 font-sans text-xs sm:text-sm">{{ streamedOutput }}</p>
              <div v-else class="flex h-full min-h-[220px] flex-col items-center justify-center text-center text-gray-500 font-sans">
                <svg class="mb-2 h-8 w-8 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M8.25 3v1.5M4.5 8.25H3m18 0h-1.5M4.5 12H3m18 0h-1.5m-15 3.75H3m18 0h-1.5M8.25 19.5V21M12 3v1.5m0 15V21m3.75-18v1.5m0 15V21m-9-1.5h10.5a2.25 2.25 0 002.25-2.25V6.75a2.25 2.25 0 00-2.25-2.25H6.75A2.25 2.25 0 004.5 6.75v10.5a2.25 2.25 0 002.25 2.25z" />
                </svg>
                <p class="text-xs">{{ pt('playground.emptyHint') }}</p>
              </div>
              <span v-if="isStreaming" class="inline-block h-3.5 w-1.5 bg-primary-400 animate-pulse ml-0.5"></span>
            </div>

            <!-- Footer Stats -->
            <div class="flex flex-wrap items-center justify-between border-t border-gray-800 bg-gray-900/60 px-4 py-2 text-[11px] text-gray-400 font-mono">
              <span>{{ pt('playground.footerModel', { model: selectedModel }) }}</span>
              <span>{{ pt('playground.footerNoBilling') }}</span>
              <span>{{ pt('playground.statusLabel', { status: statusText }) }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onBeforeUnmount } from 'vue'
import { usePortalI18n, resolvePortalLocale } from '../../i18n'
import { playgroundResponses } from '../../i18n/playgroundResponses'

const { pt, locale } = usePortalI18n()

const modelOptions = [
  { id: 'claude-3-7-sonnet', name: 'Claude 3.7 Sonnet', protocol: 'Anthropic Messages API' },
  { id: 'gpt-4o', name: 'GPT-4o', protocol: 'OpenAI Chat Completions' },
  { id: 'gemini-2.5-flash', name: 'Gemini 2.5 Flash', protocol: 'OpenAI/Gemini Protocol' },
  { id: 'deepseek-v3', name: 'DeepSeek-V3', protocol: 'OpenAI Compatible' }
]

const DEFAULT_MODEL = modelOptions[0].id
const selectedModel = ref(DEFAULT_MODEL)
const currentProtocol = computed(() => {
  return modelOptions.find(m => m.id === selectedModel.value)?.protocol || 'OpenAI Compatible'
})

const presetIds = ['architecture', 'retry', 'sse'] as const
const promptPresets = computed(() => presetIds.map(id => ({
  id,
  title: pt(`playground.presetList.${id}.title`),
  text: pt(`playground.presetList.${id}.text`)
})))

const activePrompt = ref(promptPresets.value[0].text)
const isStreaming = ref(false)
const streamedOutput = ref('')
const step = ref(0)

const statusText = computed(() => {
  if (isStreaming.value) return pt('playground.statusPlaying')
  return streamedOutput.value ? pt('playground.statusDone') : pt('playground.statusIdle')
})

let streamTimer: number | undefined
const stepTimers: number[] = []

function clearTimers() {
  window.clearInterval(streamTimer)
  streamTimer = undefined
  stepTimers.splice(0).forEach(id => window.clearTimeout(id))
}

onBeforeUnmount(clearTimers)

function resetStream() {
  clearTimers()
  isStreaming.value = false
  streamedOutput.value = ''
  step.value = 0
}

function runSimulation() {
  resetStream()
  isStreaming.value = true
  step.value = 1

  stepTimers.push(window.setTimeout(() => {
    step.value = 2
  }, 100))

  stepTimers.push(window.setTimeout(() => {
    step.value = 3

    const responses = playgroundResponses[resolvePortalLocale(locale.value)]
    const fullText = responses[selectedModel.value] || responses[DEFAULT_MODEL]
    let index = 0

    streamTimer = window.setInterval(() => {
      if (index < fullText.length) {
        const chunk = fullText.slice(index, index + 3)
        streamedOutput.value += chunk
        index += 3
      } else {
        clearTimers()
        isStreaming.value = false
      }
    }, 25)
  }, 240))
}
</script>
