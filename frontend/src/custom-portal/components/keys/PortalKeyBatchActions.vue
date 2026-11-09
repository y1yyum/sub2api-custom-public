<template>
  <div
    class="flex flex-wrap items-center gap-3 bg-gray-900 p-3 text-white dark:bg-dark-100 dark:text-dark-900"
    data-testid="key-batch-actions"
  >
    <span class="text-sm">{{ pt('refresh.selected', { count: ids.length }) }}</span>
    <button
      type="button"
      class="border border-current px-3 py-1 text-xs"
      :disabled="busy"
      @click="disable"
    >
      {{ pt('refresh.disable') }}
    </button>
    <button
      type="button"
      class="border border-current px-3 py-1 text-xs"
      :disabled="busy"
      @click="$emit('edit')"
    >
      {{ translateGlobal('keys.bulkEdit.title') }}
    </button>
    <button
      type="button"
      class="border border-current px-3 py-1 text-xs"
      :disabled="busy"
      @click="confirmDelete = true"
    >
      {{ pt('refresh.delete') }}
    </button>
    <button
      type="button"
      class="ml-auto text-xs underline"
      :disabled="busy"
      @click="$emit('clear')"
    >
      {{ translateGlobal('keys.bulkEdit.clearSelection') }}
    </button>
    <ConfirmDialog
      :show="confirmDelete"
      :title="translateGlobal('common.delete')"
      :message="pt('refresh.deleteSelected', { count: ids.length })"
      danger
      @cancel="confirmDelete = false"
      @confirm="remove"
    />
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { keysAPI } from '@/api/keys'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import { usePortalI18n } from '../../i18n'
const props = defineProps<{ ids: number[] }>()
const emit = defineEmits<{ updated: [ids: number[]]; clear: []; edit: [] }>()
const { t: translateGlobal } = useI18n()
const { pt } = usePortalI18n()
const app = useAppStore()
const busy = ref(false)
const confirmDelete = ref(false)
function finish(succeeded: number[], failed: number) {
  emit('updated', succeeded)
  if (failed) app.showError(pt('refresh.batchFailed', { count: failed }))
}
async function disable() {
  busy.value = true
  try {
    const result = await keysAPI.bulkUpdate([...props.ids], { status: 'inactive' })
    finish(result.succeededIds, result.failures.length)
  } finally {
    busy.value = false
  }
}
async function remove() {
  if (busy.value) return
  confirmDelete.value = false
  busy.value = true
  const ids = [...props.ids]
  try {
    const succeeded: number[] = []
    for (let offset = 0; offset < ids.length; offset += 5) {
      const batch = ids.slice(offset, offset + 5)
      const results = await Promise.allSettled(batch.map((id) => keysAPI.delete(id)))
      results.forEach((result, i) => {
        if (result.status === 'fulfilled') succeeded.push(batch[i])
      })
    }
    finish(succeeded, ids.length - succeeded.length)
  } finally {
    busy.value = false
  }
}
</script>
