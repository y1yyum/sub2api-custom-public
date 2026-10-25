<template>
  <ModelChannelStatusView v-if="isUnified" />
  <ChannelStatusV1View v-else-if="isV1" />
  <ChannelStatusV2View v-else />
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { isChannelMonitorV1Mode, isUnifiedModelChannelStatusEnabled } from '@/utils/featureFlags'
import ModelChannelStatusView from '@/features/model-channel-status/ModelChannelStatusView.vue'
import ChannelStatusV1View from './ChannelStatusV1View.vue'
import ChannelStatusV2View from './ChannelStatusV2View.vue'

const route = useRoute()

// `?view=classic` keeps the existing monitor reachable while the unified page is on.
// The classic page rewrites its own query on every interaction (dropping `view`),
// so the choice is latched for the lifetime of this view.
const classic = ref(false)
watch(
  () => route.query.view,
  (view) => {
    if (view === 'classic') classic.value = true
  },
  { immediate: true },
)

const isUnified = computed(() => isUnifiedModelChannelStatusEnabled() && !classic.value)
const isV1 = computed(() => isChannelMonitorV1Mode())
</script>
