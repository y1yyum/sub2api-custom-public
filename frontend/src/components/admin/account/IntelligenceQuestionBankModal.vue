<template>
  <BaseDialog :show="show" :title="t('admin.scheduledTestKinds.bank.title')" width="wide" @close="emit('close')">
    <IntelligenceQuestionBank v-if="show" @changed="(questions) => emit('changed', questions)" />
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import IntelligenceQuestionBank from './IntelligenceQuestionBank.vue'
import type { IntelligenceQuestion } from '@/types'

defineProps<{
  show: boolean
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'changed', questions: IntelligenceQuestion[]): void
}>()

const { t } = useI18n()
</script>
