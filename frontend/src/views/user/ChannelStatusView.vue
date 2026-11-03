<template>
  <ModelChannelStatusView v-if="isUnified" />
  <ChannelStatusV1View v-else-if="isV1" />
  <ChannelStatusV2View v-else />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { isChannelMonitorV1Mode, isUnifiedModelChannelStatusEnabled } from '@/utils/featureFlags'
import ModelChannelStatusView from '@/features/model-channel-status/ModelChannelStatusView.vue'
import ChannelStatusV1View from './ChannelStatusV1View.vue'
import ChannelStatusV2View from './ChannelStatusV2View.vue'

const isUnified = computed(() => isUnifiedModelChannelStatusEnabled())
const isV1 = computed(() => isChannelMonitorV1Mode())
</script>
