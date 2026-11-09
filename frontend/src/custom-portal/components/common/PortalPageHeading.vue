<template>
  <header class="portal-page-heading">
    <div class="min-w-0">
      <h1 class="portal-page-title">{{ title }}</h1>
      <p v-if="description" class="portal-page-description mt-2 text-sm text-gray-500 dark:text-dark-400">
        {{ description }}
      </p>
      <slot name="metrics" />
    </div>
    <div v-if="$slots.default" class="flex flex-wrap items-center gap-2"><slot /></div>
  </header>
</template>
<script setup lang="ts">
defineProps<{ title: string; description?: string }>()
</script>
<style scoped>
.portal-page-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 20px;
}
.portal-page-title {
  width: fit-content;
  margin: 0;
  padding-bottom: 16px;
  background: var(--pp-huiwen) left bottom / 8px 8px repeat-x;
  font: 400 24px/28px var(--pp-font-pixel);
  font-synthesis: none;
  overflow-wrap: anywhere;
}
.portal-page-description {
  padding-bottom: 20px;
  position: relative;
}
.portal-page-title:has(+ .portal-page-description) {
  padding-bottom: 0;
  background: none;
}
.portal-page-description::after {
  content: '';
  position: absolute;
  bottom: 0;
  left: 0;
  width: 96px;
  height: 8px;
  background: var(--pp-huiwen) left bottom / 8px 8px repeat-x;
}
</style>
