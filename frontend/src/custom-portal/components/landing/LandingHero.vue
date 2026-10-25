<template>
  <section class="relative min-h-[85vh] overflow-hidden pt-28 pb-20 lg:pt-36 lg:pb-28">
    <!-- Background glow & grid layers -->
    <div class="pointer-events-none absolute inset-0 -z-10 overflow-hidden">
      <!-- Top-center ambient light -->
      <div class="absolute left-1/2 -top-24 h-[500px] w-[800px] -translate-x-1/2 rounded-full bg-gradient-to-b from-primary-400/25 via-cyan-500/10 to-transparent blur-3xl dark:from-primary-600/20 dark:via-cyan-600/10"></div>
      <!-- Floating orbs -->
      <div class="absolute -left-32 top-1/4 h-80 w-80 rounded-full bg-emerald-500/10 blur-3xl"></div>
      <div class="absolute -right-32 top-1/3 h-96 w-96 rounded-full bg-primary-500/15 blur-3xl"></div>
      <!-- Subtle dot matrix pattern -->
      <div class="absolute inset-0 bg-[radial-gradient(rgba(14,165,233,0.08)_1px,transparent_1px)] bg-[size:32px_32px] dark:bg-[radial-gradient(rgba(45,212,191,0.06)_1px,transparent_1px)]"></div>
    </div>

    <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
      <div class="flex flex-col items-center text-center">
        <!-- Top Pill Badge -->
        <div class="mb-6 inline-flex items-center gap-2 rounded-full border border-primary-500/30 bg-primary-50/80 px-3.5 py-1.5 shadow-sm backdrop-blur-md dark:border-primary-500/20 dark:bg-primary-950/40">
          <span class="flex h-2 w-2 rounded-full bg-primary-400/70" aria-hidden="true"></span>
          <span class="text-xs font-semibold text-primary-800 dark:text-primary-300">
            {{ pt('hero.badge') }}
          </span>
          <svg class="h-3 w-3 text-primary-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
            <path stroke-linecap="round" stroke-linejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
          </svg>
        </div>

        <!-- Main Headline -->
        <h1 class="max-w-4xl text-4xl font-extrabold tracking-tight text-gray-900 sm:text-5xl lg:text-6xl dark:text-white">
          {{ pt('hero.headlineLead') }}
          <span class="bg-gradient-to-r from-primary-600 via-teal-500 to-cyan-500 bg-clip-text text-transparent dark:from-primary-400 dark:via-teal-300 dark:to-cyan-400">
            {{ pt('hero.headlineHighlight') }}
          </span>
        </h1>

        <!-- Subtitle -->
        <p class="mt-6 max-w-2xl text-base leading-relaxed text-gray-600 sm:text-lg dark:text-dark-300">
          {{ pt('hero.subtitleLead') }}
          <span class="font-medium text-gray-900 dark:text-white">{{ pt('hero.subtitleVendors') }}</span>
          {{ pt('hero.subtitleTail') }}
        </p>

        <!-- CTA Buttons -->
        <div class="mt-10 flex flex-wrap items-center justify-center gap-4">
          <router-link
            :to="isAuthenticated ? '/v2/dashboard' : '/register'"
            class="group inline-flex items-center gap-2 rounded-xl bg-gradient-to-r from-primary-600 to-primary-500 px-6 py-3.5 text-sm font-semibold text-white shadow-lg shadow-primary-500/25 transition-all hover:from-primary-500 hover:to-primary-600 hover:shadow-xl hover:shadow-primary-500/35 active:scale-95"
          >
            <span>{{ isAuthenticated ? pt('hero.enterConsole') : pt('hero.getKey') }}</span>
            <svg class="h-4 w-4 transition-transform group-hover:translate-x-1" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
            </svg>
          </router-link>

          <a
            href="#playground"
            class="inline-flex items-center gap-2 rounded-xl border border-gray-300/80 bg-white/80 px-6 py-3.5 text-sm font-semibold text-gray-700 shadow-sm backdrop-blur-md transition-all hover:border-gray-400 hover:bg-gray-50 active:scale-95 dark:border-dark-700 dark:bg-dark-900/80 dark:text-gray-200 dark:hover:border-dark-600 dark:hover:bg-dark-800"
          >
            <svg class="h-4 w-4 text-primary-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5.25 5.653c0-.856.917-1.398 1.667-.986l11.54 6.348a1.125 1.125 0 010 1.971l-11.54 6.347a1.125 1.125 0 01-1.667-.985V5.653z" />
            </svg>
            <span>{{ pt('hero.viewDemo') }}</span>
          </a>

          <router-link
            to="/model-plaza"
            class="inline-flex items-center gap-1.5 rounded-xl px-4 py-3.5 text-sm font-medium text-gray-600 transition-colors hover:text-primary-600 dark:text-dark-300 dark:hover:text-primary-400"
          >
            <span>{{ pt('hero.viewPricing') }}</span>
            <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 19.5l15-15m0 0H8.25m11.25 0v11.25" />
            </svg>
          </router-link>
        </div>

        <!-- Capability highlights banner -->
        <div class="mt-14 grid w-full max-w-4xl grid-cols-2 gap-3 sm:grid-cols-4 sm:gap-4">
          <div
            v-for="item in capabilityHighlights"
            :key="item"
            class="rounded-2xl border border-gray-200/60 bg-white/60 p-4 backdrop-blur-md dark:border-dark-800/80 dark:bg-dark-900/60"
          >
            <div class="text-sm font-bold tracking-tight text-gray-900 dark:text-white">{{ pt(`hero.highlights.${item}.title`) }}</div>
            <div class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ pt(`hero.highlights.${item}.detail`) }}</div>
          </div>
        </div>

        <!-- Model badges scroll/strip -->
        <div class="mt-12 flex flex-wrap items-center justify-center gap-2.5">
          <span class="text-xs font-medium text-gray-400 dark:text-dark-500 mr-2">{{ pt('hero.exampleModels') }}</span>
          <span
            v-for="model in topModels"
            :key="model.name"
            class="inline-flex items-center gap-1.5 rounded-lg border border-gray-200/60 bg-white/50 px-3 py-1 text-xs font-mono font-medium text-gray-700 shadow-sm backdrop-blur-sm dark:border-dark-700/60 dark:bg-dark-800/50 dark:text-gray-300"
          >
            <span class="h-1.5 w-1.5 rounded-full" :class="model.dot"></span>
            {{ model.name }}
          </span>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useAuthStore } from '@/stores'
import { usePortalI18n } from '../../i18n'

const { pt } = usePortalI18n()

const capabilityHighlights = ['pool', 'sse', 'protocols', 'billing'] as const

const authStore = useAuthStore()
const isAuthenticated = computed(() => authStore.isAuthenticated)

const topModels = [
  { name: 'claude-3-7-sonnet', dot: 'bg-orange-500' },
  { name: 'gpt-4o', dot: 'bg-emerald-500' },
  { name: 'gemini-2.5-pro', dot: 'bg-blue-500' },
  { name: 'deepseek-r1', dot: 'bg-indigo-500' },
  { name: 'deepseek-v3', dot: 'bg-cyan-500' },
  { name: 'grok-3', dot: 'bg-purple-500' },
  { name: 'claude-3-5-haiku', dot: 'bg-amber-500' }
]
</script>
