<template>
  <div
    class="overflow-auto rounded-xl border border-gray-200 dark:border-gray-700"
  >
    <p v-if="!items.length" class="p-8 text-center text-gray-500">
      {{ empty }}
    </p>
    <table v-else class="w-full text-left text-sm">
      <thead class="bg-gray-50 dark:bg-gray-800">
        <tr>
          <th
            v-for="key in columns"
            :key="key"
            class="whitespace-nowrap p-3 font-medium"
          >
            {{ key }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="(item, index) in items"
          :key="index"
          class="border-t border-gray-100 dark:border-gray-700"
        >
          <td
            v-for="key in columns"
            :key="key"
            class="max-w-sm whitespace-pre-wrap break-words p-3"
          >
            {{ format(item[key]) }}
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import type { RecordData } from './model'
const props = defineProps<{ items: RecordData[]; empty: string }>()
const columns = computed(() =>
  [...new Set(props.items.flatMap((item) => Object.keys(item)))].filter(
    (key) =>
      !/secret|credential|password|api_key|access_token|refresh_token/i.test(
        key,
      ),
  ),
)
function format(value: unknown): string {
  return value == null
    ? '—'
    : typeof value === 'object'
      ? JSON.stringify(value)
      : String(value)
}
</script>
