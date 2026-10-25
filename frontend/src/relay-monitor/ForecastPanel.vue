<script setup lang="ts">
import { computed } from 'vue'
import { Line } from 'vue-chartjs'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
} from 'chart.js'
import { rows, displayMoney, type RecordData } from './model'
ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
)
const props = defineProps<{
  value: unknown
  title: string
  label: (key: string) => string
}>()
const forecast = computed<RecordData>(() =>
  props.value && typeof props.value === 'object'
    ? (props.value as RecordData)
    : {},
)
const points = computed(() => rows(forecast.value, 'points'))
const chart = computed(() => ({
  labels: points.value.map((point) =>
    new Date(Number(point.t)).toLocaleString(undefined, {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    }),
  ),
  datasets: [
    {
      label: props.label('forecast'),
      data: points.value.map((point) => Number(point.cost)),
      borderColor: '#6366f1',
      pointRadius: 1,
      tension: 0.25,
    },
    {
      label: props.label('lowerBound'),
      data: points.value.map((point) =>
        point.lo == null ? null : Number(point.lo),
      ),
      borderColor: '#10b981',
      borderDash: [4, 4],
      pointRadius: 0,
      tension: 0.25,
    },
    {
      label: props.label('upperBound'),
      data: points.value.map((point) =>
        point.hi == null ? null : Number(point.hi),
      ),
      borderColor: '#f59e0b',
      borderDash: [4, 4],
      pointRadius: 0,
      tension: 0.25,
    },
  ],
}))
</script>
<template>
  <section class="card space-y-4 p-5">
    <h3 class="font-semibold">{{ title }}</h3>
    <p v-if="!points.length" class="py-12 text-center text-sm text-gray-500">
      {{ label('insufficientSamples') }}
    </p>
    <template v-else>
      <div class="h-64">
        <Line
          :data="chart"
          :options="{ responsive: true, maintainAspectRatio: false }"
        />
      </div>
      <dl class="grid grid-cols-2 gap-4 text-sm">
        <div>
          <dt class="text-xs text-gray-500">{{ label('forecastTotal') }}</dt>
          <dd class="mt-1 text-xl font-semibold">
            {{ displayMoney(forecast.nextTotal ?? forecast.next24Total) }} CNY
          </dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500">{{ label('forecastRange') }}</dt>
          <dd class="mt-1">
            {{ displayMoney(forecast.nextLo) }} –
            {{ displayMoney(forecast.nextHi) }} CNY
          </dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500">{{ label('samples') }}</dt>
          <dd class="mt-1">
            {{ displayMoney(forecast.sampleDays ?? forecast.sampleHours) }}
          </dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500">{{ label('method') }}</dt>
          <dd class="mt-1">{{ forecast.method ?? label('hourlyMethod') }}</dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500">{{ label('backtest') }}</dt>
          <dd class="mt-1">
            {{ displayMoney(forecast.backtestWapePct)
            }}{{ forecast.backtestWapePct == null ? '' : '%' }}
          </dd>
        </div>
      </dl>
      <p class="text-xs text-gray-500">{{ label('forecastNote') }}</p>
    </template>
  </section>
</template>
