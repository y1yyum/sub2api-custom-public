<!-- Adapted from TokenFlux/TokenRouter UserDashboardAnnouncements.vue (LGPL-3.0-or-later). -->
<template>
  <section class="card overflow-hidden" :aria-labelledby="titleId" data-testid="overview-announcements">
    <div class="flex min-h-[61px] items-center justify-between gap-3 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <div class="flex min-w-0 items-center gap-2.5">
        <Icon name="bell" size="md" class="shrink-0 text-primary-600 dark:text-primary-400" aria-hidden="true" />
        <h2 :id="titleId" class="truncate text-lg font-semibold text-gray-900 dark:text-white">
          {{ pt('pages.overview.announcements.title') }}
        </h2>
      </div>
      <span
        class="inline-flex shrink-0 items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium"
        :class="
          unreadCount > 0
            ? 'bg-primary-100 text-primary-800 dark:bg-primary-900/30 dark:text-primary-300'
            : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300'
        "
        data-testid="overview-announcements-unread"
      >
        <span
          class="h-1.5 w-1.5 rounded-full"
          :class="unreadCount > 0 ? 'bg-primary-500 dark:bg-primary-400' : 'bg-gray-400 dark:bg-dark-400'"
          aria-hidden="true"
        ></span>
        {{ pt('pages.overview.announcements.unread', { count: unreadCount }) }}
      </span>
    </div>

    <div class="p-6">
      <div v-if="loading && !items.length" class="space-y-5 py-4" role="status" aria-busy="true" data-testid="overview-announcements-loading">
        <div v-for="row in 3" :key="row" class="flex items-start gap-4" aria-hidden="true">
          <span class="mt-1 h-3 w-3 shrink-0 animate-pulse rounded-full bg-gray-200 motion-reduce:animate-none dark:bg-dark-700"></span>
          <span class="flex-1 space-y-3">
            <span class="block h-4 w-2/3 animate-pulse rounded bg-gray-200 motion-reduce:animate-none dark:bg-dark-700"></span>
            <span class="block h-3 w-11/12 animate-pulse rounded bg-gray-200 motion-reduce:animate-none dark:bg-dark-700"></span>
          </span>
        </div>
      </div>

      <div v-else-if="!items.length" class="flex flex-col items-center gap-2 py-8 text-center" data-testid="overview-announcements-empty">
        <Icon name="inbox" size="lg" class="text-gray-300 dark:text-dark-600" aria-hidden="true" />
        <p class="text-sm font-medium text-gray-700 dark:text-dark-200">{{ pt('pages.overview.announcements.empty') }}</p>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ pt('pages.overview.announcements.emptyHint') }}</p>
      </div>

      <ol v-else class="space-y-1" data-testid="overview-announcements-timeline">
        <li v-for="(item, index) in items" :key="item.announcement.id" class="relative pl-7">
          <span
            v-if="index < items.length - 1"
            class="absolute bottom-[-1.625rem] left-[5px] top-[1.375rem] w-px bg-gray-200 dark:bg-dark-600"
            aria-hidden="true"
          ></span>
          <span
            v-if="!item.announcement.read_at"
            class="absolute left-0 top-4 h-3 w-3 animate-ping rounded-full bg-primary-400 opacity-75 motion-reduce:animate-none"
            aria-hidden="true"
          ></span>
          <span
            class="absolute left-0 top-4 z-10 h-3 w-3 rounded-full border-[3px]"
            :class="
              item.announcement.read_at
                ? 'border-gray-200 bg-gray-400 dark:border-dark-700 dark:bg-dark-500'
                : 'border-primary-100 bg-primary-500 dark:border-dark-900 dark:bg-primary-400'
            "
            :data-unread="!item.announcement.read_at"
            aria-hidden="true"
          ></span>
          <button
            type="button"
            class="group flex min-h-[88px] w-full items-start gap-3 rounded-lg px-3 py-3 text-left transition-colors hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 motion-reduce:transition-none dark:hover:bg-dark-800/60"
            :data-testid="`overview-announcement-${item.announcement.id}`"
            @click="open(item.announcement)"
          >
            <span class="min-w-0 flex-1">
              <span
                class="line-clamp-2 block break-words text-sm font-semibold leading-5 text-gray-900 group-hover:text-primary-700 dark:text-white dark:group-hover:text-primary-300"
              >
                {{ item.announcement.title }}
              </span>
              <span class="mt-1.5 line-clamp-2 block break-words text-sm leading-5 text-gray-500 dark:text-dark-300">
                {{ item.summary || pt('pages.overview.announcements.noSummary') }}
              </span>
              <time
                :datetime="item.announcement.created_at"
                :title="formatDateTime(item.announcement.created_at)"
                class="mt-2 block text-xs text-gray-400 dark:text-dark-400"
              >
                {{ formatDay(item.announcement.created_at) }}
              </time>
            </span>
            <Icon
              name="chevronRight"
              size="sm"
              class="mt-1 shrink-0 text-gray-400 transition-transform group-hover:translate-x-0.5 group-hover:text-primary-500 motion-reduce:transition-none dark:text-dark-500"
              aria-hidden="true"
            />
          </button>
        </li>
      </ol>
    </div>

    <AnnouncementPopup :announcement="selected" preview @close="selected = null" />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, useId } from 'vue'
import { storeToRefs } from 'pinia'
import AnnouncementPopup from '@/components/common/AnnouncementPopup.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAnnouncementStore } from '@/stores/announcements'
import type { UserAnnouncement } from '@/types'
import { formatDate, formatDateTime } from '@/utils/format'
import { usePortalI18n } from '../../i18n'
import { summarizeAnnouncement } from '../../overview/announcementSummary'

const MAX_ANNOUNCEMENTS = 5

const { pt } = usePortalI18n()
const titleId = `${useId()}-title`
const store = useAnnouncementStore()
const { announcements, loading } = storeToRefs(store)
const unreadCount = computed(() => store.unreadCount)
const selected = ref<UserAnnouncement | null>(null)

const timestamp = (announcement: UserAnnouncement) => {
  const value = Date.parse(announcement.created_at)
  return Number.isNaN(value) ? 0 : value
}

// The store lists unread first; the timeline is newest first.
const items = computed(() =>
  [...announcements.value]
    .sort((a, b) => timestamp(b) - timestamp(a) || b.id - a.id)
    .slice(0, MAX_ANNOUNCEMENTS)
    .map((announcement) => ({ announcement, summary: summarizeAnnouncement(announcement.content) }))
)

function formatDay(createdAt: string): string {
  const date = new Date(createdAt)
  if (Number.isNaN(date.getTime())) return ''
  const options: Intl.DateTimeFormatOptions = { month: 'long', day: 'numeric' }
  if (date.getFullYear() !== new Date().getFullYear()) options.year = 'numeric'
  return formatDate(date, options)
}

function open(announcement: UserAnnouncement) {
  selected.value = announcement
  if (!announcement.read_at) void store.markAsRead(announcement.id)
}

defineExpose({ reload: () => store.fetchAnnouncements(true) })

onMounted(() => void store.fetchAnnouncements())
</script>
