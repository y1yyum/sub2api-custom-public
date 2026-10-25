<template>
  <section
    class="overflow-hidden rounded-2xl border bg-white shadow-card dark:bg-dark-800/50"
    :class="platformBorderStrongClass(group.platform)"
    :data-testid="`catalog-group-${group.id}`"
  >
    <h2 class="m-0">
      <button
        type="button"
        class="flex w-full flex-wrap items-center gap-x-3 gap-y-2 px-5 py-3.5 text-left transition-colors hover:bg-gray-50 focus-visible:outline focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-primary-500 dark:hover:bg-dark-700/40"
        :aria-expanded="expanded"
        :aria-controls="panelId"
        :aria-label="t('modelChannelStatus.group.toggle', { name: group.name })"
        data-testid="group-toggle"
        @click="emit('toggle', group.id)"
      >
        <Icon
          name="chevronDown"
          size="sm"
          class="shrink-0 text-gray-400 transition-transform"
          :class="expanded ? '' : '-rotate-90'"
        />
        <GroupBadge
          :name="group.name"
          :platform="group.platform as GroupPlatform"
          :rate-multiplier="group.rate_multiplier"
          :user-rate-multiplier="group.user_rate_multiplier"
          :peak-rate-enabled="group.peak_rate_enabled"
          :peak-start="group.peak_start"
          :peak-end="group.peak_end"
          :peak-rate-multiplier="group.peak_rate_multiplier"
          always-show-rate
        />
        <span
          v-if="group.is_exclusive"
          class="inline-flex items-center rounded-md bg-purple-50 px-2 py-0.5 text-xs font-medium text-purple-600 dark:bg-purple-900/20 dark:text-purple-400"
        >
          {{ t('modelChannelStatus.group.exclusive') }}
        </span>
        <span class="ml-auto flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-dark-400">
          <span data-testid="group-model-count">{{ t('modelChannelStatus.group.models', { count: group.models.length }) }}</span>
          <span v-if="group.summary.down_count > 0" class="font-medium text-red-600 dark:text-red-400">
            {{ t('modelChannelStatus.group.down', { count: group.summary.down_count }) }}
          </span>
          <span v-if="group.summary.degraded_count > 0" class="font-medium text-amber-600 dark:text-amber-400">
            {{ t('modelChannelStatus.group.degraded', { count: group.summary.degraded_count }) }}
          </span>
          <span class="inline-flex items-center gap-1.5 font-medium" :class="healthTextClass(group.summary.health)" data-testid="group-health">
            <span class="h-2 w-2 rounded-full" :class="healthBlockClass(group.summary.health)" aria-hidden="true" />
            {{ t(`modelChannelStatus.health.${group.summary.health}`) }}
          </span>
          <span v-if="group.summary.availability_rate != null" class="tabular-nums" data-testid="group-availability">
            {{ t('modelChannelStatus.group.availability', { rate: formatPercent(group.summary.availability_rate) }) }}
          </span>
        </span>
      </button>
    </h2>
    <ul v-show="expanded" :id="panelId" class="m-0 list-none divide-y divide-gray-100 border-t border-gray-100 p-0 dark:divide-dark-700/60 dark:border-dark-700/60">
      <CatalogModelRow
        v-for="row in group.models"
        :key="row.key"
        :row="row"
        :selected="row.key === selectedKey"
        @select="emit('select', $event, group)"
      />
    </ul>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import { platformBorderStrongClass } from '@/utils/platformColors'
import type { GroupPlatform } from '@/types'
import type { CatalogGroup, CatalogModelRow as CatalogRow } from '@/api/channelStatusCatalog'
import CatalogModelRow from './CatalogModelRow.vue'
import { formatPercent, healthBlockClass, healthTextClass } from './catalogFormat'

const props = defineProps<{
  group: CatalogGroup
  expanded: boolean
  selectedKey?: string | null
}>()
const emit = defineEmits<{
  toggle: [groupId: number]
  select: [row: CatalogRow, group: CatalogGroup]
}>()

const { t } = useI18n()
const panelId = `catalog-group-panel-${props.group.id}`
</script>
