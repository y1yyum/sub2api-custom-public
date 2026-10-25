<template>
  <section id="features" class="relative py-20 bg-gray-50/50 dark:bg-dark-950/50">
    <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
      <!-- Section Title -->
      <div class="mx-auto max-w-3xl text-center">
        <div class="inline-flex items-center gap-1.5 rounded-full bg-primary-100/80 px-3 py-1 text-xs font-semibold text-primary-700 dark:bg-primary-950/60 dark:text-primary-300">
          <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 013.598 6 11.99 11.99 0 003 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285z" />
          </svg>
          {{ pt('features.badge') }}
        </div>
        <h2 class="mt-3 text-3xl font-bold tracking-tight text-gray-900 sm:text-4xl dark:text-white">
          {{ pt('features.title') }}
        </h2>
        <p class="mt-3 text-base text-gray-600 dark:text-dark-300">
          {{ pt('features.description') }}
        </p>
      </div>

      <!-- Features Grid -->
      <div class="mt-14 grid grid-cols-1 gap-6 sm:grid-cols-2 lg:grid-cols-3">
        <div
          v-for="feat in featureList"
          :key="feat.id"
          class="group relative overflow-hidden rounded-2xl border border-gray-200/80 bg-white/80 p-6 shadow-sm backdrop-blur-xl transition-all duration-300 hover:-translate-y-1 hover:border-primary-500/40 hover:shadow-xl dark:border-dark-700/80 dark:bg-dark-900/60"
        >
          <div class="mb-4 inline-flex h-12 w-12 items-center justify-center rounded-xl transition-transform group-hover:scale-110" :class="feat.iconBg">
            <span class="text-xl">{{ feat.icon }}</span>
          </div>
          <h3 class="text-base font-bold text-gray-900 dark:text-white">
            {{ pt(`features.list.${feat.id}.title`) }}
          </h3>
          <p class="mt-2 text-xs leading-relaxed text-gray-600 dark:text-dark-400">
            {{ pt(`features.list.${feat.id}.description`) }}
          </p>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { usePortalI18n } from '../../i18n'

const { pt } = usePortalI18n()

const featureList = [
  { id: 'pool', icon: '⚡', iconBg: 'bg-amber-100 text-amber-700 dark:bg-amber-950/60 dark:text-amber-400' },
  { id: 'protocols', icon: '🔄', iconBg: 'bg-primary-100 text-primary-700 dark:bg-primary-950/60 dark:text-primary-400' },
  { id: 'failover', icon: '🛡️', iconBg: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-400' },
  { id: 'billing', icon: '📊', iconBg: 'bg-indigo-100 text-indigo-700 dark:bg-indigo-950/60 dark:text-indigo-400' },
  { id: 'keys', icon: '🔑', iconBg: 'bg-cyan-100 text-cyan-700 dark:bg-cyan-950/60 dark:text-cyan-400' },
  { id: 'outbound', icon: '🌐', iconBg: 'bg-rose-100 text-rose-700 dark:bg-rose-950/60 dark:text-rose-400' }
] as const
</script>
