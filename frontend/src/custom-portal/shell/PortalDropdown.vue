<template>
  <div ref="rootRef" class="relative" @keydown.esc="close">
    <button
      type="button"
      :class="triggerClass"
      :aria-label="label"
      :aria-expanded="open"
      aria-haspopup="menu"
      :data-testid="testId"
      @click="toggle"
    >
      <slot name="trigger" />
    </button>
    <transition name="portal-dropdown">
      <div v-if="open" class="dropdown mt-2" :class="panelClass" role="menu" @click="onPanelClick">
        <slot :close="close" />
      </div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

withDefaults(
  defineProps<{
    label: string
    triggerClass?: string
    panelClass?: string
    testId?: string
  }>(),
  { triggerClass: '', panelClass: 'right-0 w-56', testId: undefined }
)

const emit = defineEmits<{ open: [] }>()

const open = ref(false)
const rootRef = ref<HTMLElement | null>(null)
const route = useRoute()

function toggle() {
  open.value = !open.value
  if (open.value) emit('open')
}

function close() {
  open.value = false
}

function onPanelClick(event: MouseEvent) {
  if ((event.target as HTMLElement).closest('a')) close()
}

function onDocumentClick(event: MouseEvent) {
  if (rootRef.value && !rootRef.value.contains(event.target as Node)) close()
}

watch(() => route.fullPath, close)
onMounted(() => document.addEventListener('click', onDocumentClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocumentClick))
</script>

<style scoped>
.portal-dropdown-enter-active,
.portal-dropdown-leave-active {
  transition: opacity 0.15s ease, transform 0.15s ease;
}

.portal-dropdown-enter-from,
.portal-dropdown-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
