<template>
  <div
    class="relative overflow-hidden rounded-2xl border border-gray-200/70 bg-white/70 backdrop-blur-xl transition-all duration-300 dark:border-dark-700/60 dark:bg-dark-900/60"
    :class="[
      hoverEffect ? 'hover:-translate-y-1 hover:border-primary-500/40 hover:shadow-xl hover:shadow-primary-500/10 dark:hover:border-primary-400/30' : '',
      interactive ? 'cursor-pointer' : '',
      paddings
    ]"
  >
    <!-- Subtle top highlight line -->
    <div class="pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-white/80 to-transparent dark:via-white/10"></div>
    <slot />
  </div>
</template>

<script setup lang="ts">
withDefaults(
  defineProps<{
    hoverEffect?: boolean
    interactive?: boolean
    paddings?: string
  }>(),
  {
    hoverEffect: true,
    interactive: false,
    paddings: 'p-6'
  }
)
</script>
