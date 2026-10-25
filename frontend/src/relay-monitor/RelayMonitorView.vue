<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
} from 'chart.js'
import { Line } from 'vue-chartjs'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from './DataTable.vue'
import ForecastPanel from './ForecastPanel.vue'
import { messages } from './messages'
import { read, write } from './api'
import {
  displayMoney,
  rows,
  trendRows,
  stationPayload,
  tokenPayload,
  type RecordData,
  type Station,
  type Token,
} from './model'
ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
)
const { locale } = useI18n({ useScope: 'global' })
const labels = computed(
  () => messages[locale.value.startsWith('zh') ? 'zh' : 'en'],
)
const label = (key: string) =>
  labels.value[key as keyof typeof labels.value] || key
const tabs = [
  'stations',
  'overview',
  'usage',
  'analytics',
  'own',
  'notifications',
  'reports',
  'settings',
]
const tab = ref('stations')
const stations = ref<Station[]>([])
const selected = ref<Station | null>(null)
const tokens = ref<Token[]>([])
const data = ref<RecordData>({})
const channels = ref<RecordData[]>([])
const subjects = ref<RecordData[]>([])
const deliveries = ref<RecordData[]>([])
const purchases = ref<RecordData[]>([])
const audit = ref<RecordData[]>([])
const userBalances = ref<RecordData[]>([])
const localSubject = computed(() => !filterSubject.value || subjects.value.find(s => String(s.id) === filterSubject.value)?.type === 'local')
const purchaseAmount = ref('')
const purchaseStart = ref('')
const purchaseEnd = ref('')
const rules = reactive<Record<string, number[]>>({
  low: [],
  exhaust: [],
  error: [],
  recover: [],
  eta: [],
  report: [],
})
const settings = reactive<RecordData>({
  enabled: false,
  refresh_seconds: 60,
  timezone: 'Asia/Shanghai',
  retention_days: 90,
  daily_report_enabled: false,
  daily_report_time: '09:00',
  low_balance: '5',
  error_threshold: 3,
  eta_hours: 72,
  cooldown_minutes: 1440,
})
const busy = ref(false)
const error = ref('')
const notice = ref('')
const encryptionConfigured = ref(false)
const windowValue = ref('today')
const days = ref(7)
const dimension = ref('model')
const filterStation = ref('')
const filterToken = ref('')
const filterSubject = ref('')
const start = ref('')
const end = ref('')
const reportDate = ref(new Date().toLocaleDateString('en-CA'))
const adminKeys = ref('')
const modal = ref('')
const editId = ref<number | null>(null)
const form = reactive<RecordData>({})
const jsonConfig = ref('{}')
const jsonSecret = ref('')
const aliasText = ref('')
const secret = reactive<Record<string, string>>({})
const channelConfig = reactive<Record<string, string>>({})
const channelSchema: Record<string, { config: string[]; secret: string[] }> = {
  telegram: { config: ['chatId'], secret: ['botToken', 'apiBase'] },
  dingtalk: { config: [], secret: ['webhook', 'secret'] },
  wecom: { config: [], secret: ['webhook'] },
  feishu: { config: [], secret: ['webhook', 'secret'] },
  bark: { config: [], secret: ['server', 'deviceKey'] },
  ntfy: { config: ['topic'], secret: ['server', 'token'] },
  serverchan: { config: [], secret: ['sendKey'] },
  resend: { config: ['from', 'to'], secret: ['apiKey'] },
  smtp: {
    config: ['host', 'port', 'username', 'from', 'to', 'secure'],
    secret: ['password'],
  },
  webhook: { config: [], secret: ['url', 'headersJson'] },
}
const selectedChannelSchema = computed(
  () => channelSchema[String(form.type)] || { config: [], secret: [] },
)
const credentialFields = [
  'token',
  'api_key',
  'user_id',
  'email',
  'password',
  'username',
  'access_token',
  'refresh_token',
]
const stationTypes = [
  'newapi',
  'newapi-key',
  'sub2api',
  'sub2api-password',
  'jucode',
  'monoize',
  'monoize-key',
  'fixed',
]
const channelTypes = [
  'telegram',
  'dingtalk',
  'wecom',
  'feishu',
  'bark',
  'ntfy',
  'serverchan',
  'resend',
  'smtp',
  'webhook',
]
const stationFields = [
  'name',
  'url',
  'type',
  'enabled',
  'cny_per_usd',
  'balance_mode',
  'balance_token_id',
  'low_balance',
  'no_renewal',
  'include_in_cost',
  'subject_id',
]
const tokenFields = ['name', 'account_id', 'enabled', 'cny_per_usd_override']
const subjectFields = [
  'name',
  'type',
  'station_id',
  'admin_user_ids',
  'resold_api_key_ids',
  'include_local_cost',
  'cny_per_usd',
]
const booleanFields = new Set([
  'enabled',
  'no_renewal',
  'include_in_cost',
  'daily_report_enabled',
  'include_local_cost',
])
const numberFields = new Set([
  'refresh_seconds',
  'retention_days',
  'error_threshold',
  'eta_hours',
  'cooldown_minutes',
  'account_id',
  'balance_token_id',
  'subject_id',
  'station_id',
])
const fields = computed(() =>
  modal.value === 'station'
    ? stationFields
    : modal.value === 'token'
      ? tokenFields
      : modal.value === 'subject'
        ? subjectFields
        : ['name', 'type', 'enabled'],
)
const tableRows = computed(() =>
  rows(data.value, tab.value === 'overview' ? 'stations' : 'rows'),
)
const chartRows = computed(() =>
  trendRows(data.value.trend).length
    ? trendRows(data.value.trend)
    : tableRows.value,
)
const chartData = computed(() => ({
  labels: chartRows.value.map((row, i) =>
    String(row.date ?? row.day ?? row.time ?? row.created_at ?? i + 1),
  ),
  datasets: [
    {
      label: label('cost'),
      data: chartRows.value.map((row) =>
        row.cost_cny == null && row.total_cost_cny == null
          ? null
          : Number(row.cost_cny ?? row.total_cost_cny),
      ),
      borderColor: '#6366f1',
      backgroundColor: '#6366f122',
      tension: 0.3,
      spanGaps: false,
    },
  ],
}))
async function run(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await action()
  } catch (e) {
    error.value = e instanceof Error ? e.message : label('error')
  } finally {
    busy.value = false
  }
}
async function loadStations() {
  stations.value =
    (await read<{ stations: Station[] }>('/stations')).stations || []
  if (selected.value)
    selected.value =
      stations.value.find((station) => station.id === selected.value!.id) ||
      null
}
async function loadTokens() {
  if (!selected.value) return
  const [accounts, costs] = await Promise.all([
    read<{ tokens: Token[] }>(`/stations/${selected.value.id}/tokens`),
    read<unknown>(`/stations/${selected.value.id}/purchases`),
  ])
  tokens.value = accounts.tokens || []
  purchases.value = rows(costs, 'purchases')
}
async function loadPanel() {
  if (tab.value === 'stations') {
    await loadStations()
    await loadTokens()
    return
  }
  if (tab.value === 'settings') {
    const value = await read('/settings')
    encryptionConfigured.value = value.encryption_key_configured === true
    const { encryption_key_configured: _key, ...editable } = value
    Object.assign(settings, editable)
    return
  }
  if (tab.value === 'notifications') {
    const [c, r, d] = await Promise.all([
      read('/notifications/channels'),
      read('/notifications/rules'),
      read('/notifications/deliveries'),
    ])
    channels.value = rows(c, 'channels')
    for (const event of Object.keys(rules))
      rules[event] = Array.isArray(r[event]) ? (r[event] as number[]) : []
    deliveries.value = rows(d, 'deliveries')
    return
  }
  if (tab.value === 'own') {
    const [a, s, k] = await Promise.all([
      read('/own/analytics', {
        window: windowValue.value,
        dimension: dimension.value,
        subject_id: filterSubject.value || undefined,
      }),
      read('/own/subjects'),
      read('/own/admin-keys'),
    ])
    data.value = a
    subjects.value = rows(s, 'subjects')
    userBalances.value = localSubject.value ? rows(await read('/own/user-balances'), 'rows') : []
    adminKeys.value = Array.isArray(k.api_key_ids)
      ? k.api_key_ids.join('\n')
      : ''
    return
  }
  const params: RecordData = { window: windowValue.value, days: days.value }
  if (filterStation.value) params.station_id = Number(filterStation.value)
  if (filterToken.value) params.token_id = Number(filterToken.value)
  if (filterSubject.value) params.subject_id = Number(filterSubject.value)
  if (tab.value === 'reports') params.date = reportDate.value
  data.value = await read(
    tab.value === 'reports' ? '/reports/preview' : '/' + tab.value,
    params,
  )
}
async function selectStation(station: Station) {
  if (busy.value) return
  selected.value = station
  await run(loadTokens)
}
function open(kind: string, item?: RecordData) {
  modal.value = kind
  editId.value = item ? Number(item.id) : null
  Object.keys(form).forEach((key) => delete form[key])
  Object.keys(secret).forEach((key) => delete secret[key])
  Object.keys(channelConfig).forEach((key) => delete channelConfig[key])
  Object.assign(channelConfig, item?.config || {})
  const defaults =
    kind === 'station'
      ? {
          name: '',
          url: '',
          type: 'newapi',
          enabled: true,
          cny_per_usd: '1',
          balance_mode: 'shared',
          balance_token_id: null,
          low_balance: '5',
          no_renewal: false,
          include_in_cost: true,
          subject_id: null,
        }
      : kind === 'token'
        ? {
            name: '',
            account_id: null,
            enabled: true,
            cny_per_usd_override: '',
          }
        : kind === 'subject'
          ? {
              name: '',
              type: 'local',
              station_id: null,
              admin_user_ids: [],
              resold_api_key_ids: [],
              include_local_cost: true,
              cny_per_usd: '1',
            }
          : { name: '', type: 'telegram', enabled: true }
  Object.assign(form, defaults)
  if (item) {
    for (const key of [...fields.value, 'version'])
      if (key in item) form[key] = item[key]
  }
  aliasText.value = Array.isArray(item?.channel_aliases)
    ? item.channel_aliases.join(', ')
    : ''
  jsonConfig.value = JSON.stringify(item?.config || {}, null, 2)
  jsonSecret.value = ''
}
async function save() {
  await run(async () => {
    const body: RecordData = { ...form }
    for (const key of numberFields)
      if (key in body)
        body[key] =
          body[key] === '' || body[key] == null ? null : Number(body[key])
    let path = ''
    if (modal.value === 'station') {
      body.channel_aliases = aliasText.value
        .split(',')
        .map((x) => x.trim())
        .filter(Boolean)
      path = '/stations'
      Object.assign(body, stationPayload(body))
    }
    if (modal.value === 'token') {
      if (!selected.value) return
      path = `/stations/${selected.value.id}/tokens`
      Object.assign(body, tokenPayload({ ...body, credential: secret }))
    }
    if (modal.value === 'channel') {
      path = '/notifications/channels'
      body.config = { ...JSON.parse(jsonConfig.value), ...channelConfig }
      const secrets = Object.fromEntries(
        Object.entries(secret).filter(([, value]) => value !== ''),
      )
      if (jsonSecret.value.trim())
        Object.assign(secrets, JSON.parse(jsonSecret.value))
      if (Object.keys(secrets).length) body.secret = secrets
    }
    if (modal.value === 'subject') {
      path = '/own/subjects'
      for (const key of ['admin_user_ids', 'resold_api_key_ids'])
        if (typeof body[key] === 'string')
          body[key] = String(body[key])
            .split(',')
            .map((x) => Number(x.trim()))
            .filter((x) => Number.isInteger(x) && x > 0)
    }
    await write(
      path + (editId.value === null ? '' : '/' + editId.value),
      body,
      editId.value === null ? 'post' : 'put',
    )
    modal.value = ''
    await loadPanel()
    notice.value = label('success')
  })
}
async function remove(path: string) {
  if (!confirm(label('confirmDelete'))) return
  await run(async () => {
    await write(path, {}, 'delete')
    await loadPanel()
  })
}
async function refresh() {
  await run(async () => {
    await write(
      selected.value && tab.value === 'stations'
        ? `/stations/${selected.value.id}/refresh`
        : '/refresh',
    )
    await loadPanel()
  })
}
async function saveSettings() {
  await run(async () => {
    await write('/settings', { ...settings }, 'put')
    notice.value = label('success')
  })
}
async function saveRules() {
  await run(async () => {
    await write('/notifications/rules', { ...rules }, 'put')
    notice.value = label('success')
  })
}
async function saveKeys() {
  await run(async () => {
    const ids = adminKeys.value
      .split(/[\s,]+/)
      .filter(Boolean)
      .map(Number)
    if (ids.some((x) => !Number.isInteger(x) || x <= 0))
      throw new Error('Invalid API key ID')
    await write('/own/admin-keys', { api_key_ids: ids }, 'put')
    notice.value = label('success')
  })
}
async function history() {
  if (!selected.value) return
  await run(async () => {
    data.value = await read(`/stations/${selected.value!.id}/history`, {
      start: start.value ? new Date(start.value).toISOString() : undefined,
      end: end.value ? new Date(end.value).toISOString() : undefined,
    })
  })
}
async function savePurchase() {
  if (!selected.value) return
  await run(async () => {
    await write(`/stations/${selected.value!.id}/purchases`, {
      amount_cny: purchaseAmount.value,
      start_at: new Date(purchaseStart.value).toISOString(),
      end_at: new Date(purchaseEnd.value).toISOString(),
    })
    await loadTokens()
    purchaseAmount.value = ''
    notice.value = label('success')
  })
}
async function loadAudit() {
  await run(async () => {
    const value = await read('/own/audit', {
      window: windowValue.value,
      dimension: dimension.value,
      subject_id: filterSubject.value || undefined,
    })
    audit.value = rows(value, 'records')
  })
}
async function sendReport() {
  if (!confirm(label('confirmSend'))) return
  await run(async () => {
    await write('/reports/send', { date: reportDate.value })
    notice.value = label('success')
  })
}
watch(tab, () => {
  data.value = {}
  void run(loadPanel)
})
let refreshTimer: ReturnType<typeof setInterval> | undefined
onMounted(() =>
  run(async () => {
    await loadStations()
    const value = await read('/settings')
    encryptionConfigured.value = value.encryption_key_configured === true
    Object.assign(settings, value)
    refreshTimer = setInterval(() => {
      if (tab.value === 'overview' && !busy.value && !modal.value && settings.enabled) void run(loadPanel)
    }, Math.max(10, Number(settings.refresh_seconds) || 60) * 1000)
  }),
)
onUnmounted(() => { if (refreshTimer) clearInterval(refreshTimer) })
</script>

<template>
  <AppLayout>
    <div class="relay-monitor space-y-6">
      <header class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <p
            class="text-xs font-semibold uppercase tracking-widest text-indigo-500"
          >
            RELAY MONITOR
          </p>
          <h1 class="mt-1 text-2xl font-bold">{{ label('title') }}</h1>
          <p class="mt-1 text-sm text-gray-500">{{ label('subtitle') }}</p>
        </div>
        <button class="btn-primary" :disabled="busy" @click="refresh">
          ↻ {{ label('refresh') }}
        </button>
      </header>
      <div
        v-if="error"
        role="alert"
        class="flex justify-between rounded-xl bg-red-50 p-4 text-red-700 dark:bg-red-950"
      >
        {{ error }}<button @click="run(loadPanel)">{{ label('retry') }}</button>
      </div>
      <div
        v-if="notice"
        role="status"
        class="rounded-xl bg-emerald-50 p-4 text-emerald-700 dark:bg-emerald-950"
      >
        {{ notice }}
      </div>
      <nav
        class="flex gap-1 overflow-auto border-b border-gray-200 pb-2 dark:border-gray-700"
        :aria-label="label('title')"
      >
        <button
          v-for="item in tabs"
          :key="item"
          :disabled="busy"
          :aria-current="tab === item ? 'page' : undefined"
          class="whitespace-nowrap rounded-lg px-4 py-2 text-sm"
          :class="
            tab === item
              ? 'bg-indigo-600 text-white'
              : 'text-gray-500 hover:bg-gray-100 dark:hover:bg-gray-800'
          "
          @click="tab = item"
        >
          {{ label(item) }}
        </button>
      </nav>
      <p v-if="busy" role="status" class="text-sm text-indigo-500">
        {{ label('loading') }}
      </p>
      <template v-if="tab === 'stations'">
        <div class="grid grid-cols-2 gap-4">
          <div class="card p-5">
            <p class="text-sm text-gray-500">{{ label('total') }}</p>
            <p class="mt-2 text-3xl font-semibold">{{ stations.length }}</p>
          </div>
          <div class="card p-5">
            <p class="text-sm text-gray-500">{{ label('active') }}</p>
            <p class="mt-2 text-3xl font-semibold text-emerald-500">
              {{ stations.filter((s) => s.enabled).length }}
            </p>
          </div>
        </div>
        <div class="flex justify-between">
          <h2 class="text-lg font-semibold">{{ label('stations') }}</h2>
          <button
            data-test="add-station"
            class="btn-primary"
            @click="open('station')"
          >
            + {{ label('add') }}
          </button>
        </div>
        <p v-if="!stations.length" class="card p-12 text-center text-gray-500">
          {{ label('empty') }}
        </p>
        <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          <article
            v-for="station in stations"
            :key="station.id"
            class="card cursor-pointer space-y-4 p-5"
            :class="selected?.id === station.id ? 'ring-2 ring-indigo-500' : ''"
            @click="selectStation(station)"
          >
            <div class="flex justify-between gap-2">
              <h3 class="font-semibold">{{ station.name }}</h3>
              <span
                class="rounded-full px-2 py-1 text-xs"
                :class="
                  station.enabled
                    ? 'bg-emerald-100 text-emerald-700'
                    : 'bg-gray-100 text-gray-500'
                "
                >{{ station.enabled ? label('enabled') : '—' }}</span
              >
            </div>
            <p class="truncate text-xs text-gray-500">
              {{ station.type }} · {{ station.url }}
            </p>
            <p class="text-2xl font-semibold">
              {{
                displayMoney(
                  station.balance ??
                    (station.latest as RecordData | undefined)?.remaining_cny,
                )
              }}
            </p>
            <p class="text-xs text-gray-500">
              {{ label('rate') }}: {{ station.cny_per_usd }} CNY/USD ·
              {{ station.balance_mode }}
            </p>
            <div class="flex gap-3 text-sm">
              <button @click.stop="open('station', station)">
                {{ label('edit') }}</button
              ><button
                class="text-red-500"
                @click.stop="remove('/stations/' + station.id)"
              >
                {{ label('remove') }}
              </button>
            </div>
          </article>
        </div>
        <section v-if="selected" class="card space-y-4 p-5">
          <div class="flex justify-between">
            <h2 class="font-semibold">
              {{ selected.name }} · {{ label('tokens') }}
            </h2>
            <button class="btn-primary" @click="open('token')">
              + {{ label('add') }}
            </button>
          </div>
          <p class="text-sm text-gray-500">{{ label('rateHelp') }}</p>
          <p v-if="!encryptionConfigured" class="text-sm text-amber-600">
            {{ label('disabledKey') }}
          </p>
          <div
            v-for="token in tokens"
            :key="token.id"
            class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 py-4 dark:border-gray-700"
          >
            <div>
              <p class="font-medium">{{ token.name }}</p>
              <p class="text-xs text-gray-500">
                {{ label('rate') }}: {{ token.effective_cny_per_usd }} CNY/USD ·
                {{
                  token.rate_source === 'token'
                    ? label('override')
                    : label('inherit')
                }}
                · {{ token.has_credential ? '●' : '○' }}
              </p>
            </div>
            <div class="flex gap-3 text-sm">
              <button
                :data-test="'edit-token-' + token.id"
                @click="open('token', token)"
              >
                {{ label('edit') }}</button
              ><button
                class="text-red-500"
                @click="remove(`/stations/${selected.id}/tokens/${token.id}`)"
              >
                {{ label('remove') }}
              </button>
            </div>
          </div>
          <p v-if="!tokens.length" class="py-6 text-center text-gray-500">
            {{ label('noData') }}
          </p>
          <div class="flex flex-wrap items-end gap-3">
            <label
              >{{ label('start')
              }}<input v-model="start" type="datetime-local" /></label
            ><label
              >{{ label('end')
              }}<input v-model="end" type="datetime-local" /></label
            ><button class="btn-secondary" @click="history">
              {{ label('history') }}
            </button>
          </div>
          <DataTable
            v-if="Object.keys(data).length"
            :items="rows(data, 'history')"
            :empty="label('noData')"
          />
        </section>
        <section v-if="selected" class="card space-y-4 p-5">
          <h2 class="font-semibold">{{ label('purchases') }}</h2>
          <form
            class="flex flex-wrap items-end gap-3"
            @submit.prevent="savePurchase"
          >
            <label
              >{{ label('amount_cny')
              }}<input
                v-model="purchaseAmount"
                required
                pattern="\d+(\.\d+)?" /></label
            ><label
              >{{ label('start')
              }}<input
                v-model="purchaseStart"
                type="datetime-local"
                required /></label
            ><label
              >{{ label('end')
              }}<input
                v-model="purchaseEnd"
                type="datetime-local"
                required /></label
            ><button class="btn-primary" :disabled="busy">
              {{ label('add') }}
            </button>
          </form>
          <div
            v-for="purchase in purchases"
            :key="String(purchase.id)"
            class="flex flex-wrap justify-between gap-3 border-t py-3 text-sm"
          >
            <span
              >{{ purchase.amount_cny }} CNY · {{ purchase.start_at }} →
              {{ purchase.end_at }}</span
            ><button
              class="text-red-500"
              @click="
                remove(`/stations/${selected.id}/purchases/${purchase.id}`)
              "
            >
              {{ label('remove') }}
            </button>
          </div>
        </section>
      </template>
      <template
        v-else-if="['overview', 'usage', 'analytics', 'own'].includes(tab)"
      >
        <div class="flex flex-wrap items-end gap-3">
          <label
            >{{ label('window')
            }}<select v-model="windowValue">
              <option
                v-for="value in ['today', '24h', '7d', '30d']"
                :key="value"
              >
                {{ value }}
              </option>
            </select></label
          ><label v-if="tab === 'analytics'"
            >{{ label('days')
            }}<select v-model="days">
              <option v-for="value in [7, 14, 30]" :key="value" :value="value">
                {{ value }}
              </option>
            </select></label
          ><label v-if="tab !== 'own'"
            >{{ label('stations')
            }}<select v-model="filterStation">
              <option value="">{{ label('all') }}</option>
              <option v-for="s in stations" :key="s.id" :value="s.id">
                {{ s.name }}
              </option>
            </select></label
          ><label v-if="tab === 'usage'"
            >Token ID<input
              v-model="filterToken"
              type="number"
              min="1" /></label
          ><label v-if="tab === 'analytics' || tab === 'own'"
            >{{ label('subject_id')
            }}<input v-model="filterSubject" type="number" min="1" /></label
          ><label v-if="tab === 'own'"
            >{{ label('dimension')
            }}<select v-model="dimension">
              <option
                v-for="value in [
                  'model',
                  'user',
                  'group',
                  'channel',
                  'user_group',
                ]"
                :key="value"
              >
                {{ value }}
              </option>
            </select></label
          ><button
            class="btn-secondary"
            :disabled="busy"
            @click="run(loadPanel)"
          >
            {{ label('refresh') }}
          </button>
        </div>
        <div class="grid gap-4 sm:grid-cols-3">
          <div
            v-for="key in tab === 'overview'
              ? [
                  'balance_cny',
                  'today_cost_cny',
                  'average_daily_cost_cny',
                  'low_count',
                  'exhausted_count',
                  'error_count',
                  'unknown_count',
                ]
              : [
                  'total_cost_cny',
                  'fixed_cost_cny',
                  'wallet_revenue_cny',
                  'profit_cny',
                  'partial',
                ]"
            :key="key"
            class="card p-5"
          >
            <p class="text-xs text-gray-500">{{ label(key) }}</p>
            <p class="mt-2 text-2xl font-semibold">
              {{ displayMoney(data[key]) }}
            </p>
          </div>
        </div>
        <div v-if="chartRows.length" class="card h-72 p-5">
          <Line
            :data="chartData"
            :options="{ responsive: true, maintainAspectRatio: false }"
          />
        </div>
        <p
          v-if="data.profit_note || data.subscription_note"
          class="text-sm text-amber-600"
        >
          {{ data.profit_note ?? data.subscription_note }}
        </p>
        <p v-if="data.trend_estimated" class="text-xs text-gray-500">
          {{ label('estimated') }}
        </p>
        <DataTable
          v-if="tab !== 'overview'"
          :items="tableRows"
          :empty="label('noData')"
        />
        <div v-if="tab === 'overview'" class="grid gap-4 lg:grid-cols-2">
          <article
            v-for="station in tableRows"
            :key="String(station.id)"
            class="card space-y-4 p-5"
          >
            <div class="flex justify-between">
              <h3 class="font-semibold">{{ station.name }}</h3>
              <span class="text-xs text-amber-600">{{
                station.stale ? label('stale') : ''
              }}</span>
            </div>
            <dl class="grid grid-cols-2 gap-4 text-sm">
              <div>
                <dt class="text-gray-500">{{ label('balance_cny') }}</dt>
                <dd class="mt-1 text-xl font-semibold">
                  {{ displayMoney(station.balance_cny) }}
                </dd>
              </div>
              <div>
                <dt class="text-gray-500">{{ label('burnPerDay') }}</dt>
                <dd class="mt-1">
                  {{
                    displayMoney(
                      (station.forecast as RecordData | null)?.burnPerDay,
                    )
                  }}
                  CNY
                </dd>
              </div>
              <div>
                <dt class="text-gray-500">{{ label('etaDays') }}</dt>
                <dd class="mt-1">
                  {{
                    displayMoney(
                      (station.forecast as RecordData | null)?.etaDays,
                    )
                  }}
                </dd>
              </div>
              <div>
                <dt class="text-gray-500">{{ label('etaAt') }}</dt>
                <dd class="mt-1">
                  {{
                    displayMoney((station.forecast as RecordData | null)?.etaAt)
                  }}
                </dd>
              </div>
            </dl>
            <p v-if="!station.forecast" class="text-xs text-gray-500">
              {{ label('insufficientSamples') }}
            </p>
            <p v-else class="text-xs text-gray-500">
              {{ label('method') }}:
              {{ (station.forecast as RecordData).basis }} ·
              {{ label('samples') }}:
              {{ (station.forecast as RecordData).samples }} ·
              {{ (station.forecast as RecordData).spanHours }} h
            </p>
          </article>
        </div>
        <template v-if="tab === 'analytics'"
          ><h2 class="font-semibold">{{ label('heatmap') }}</h2>
          <div class="flex flex-wrap gap-1">
            <span
              v-for="(cell, index) in rows(data, 'heatmap')"
              :key="index"
              class="h-5 w-5 rounded bg-indigo-500"
              :style="{
                opacity: Math.max(
                  0.1,
                  Math.min(
                    1,
                    Number(cell.cost_cny) /
                      Math.max(
                        1,
                        ...rows(data, 'heatmap').map((x) => Number(x.cost_cny)),
                      ),
                  ),
                ),
              }"
              :title="String(cell.time) + ': ' + cell.cost_cny + ' CNY'"
            />
          </div>
          <h2 class="font-semibold">{{ label('forecast') }}</h2>
          <div class="grid gap-4 xl:grid-cols-2">
            <ForecastPanel
              :value="data.forecast_daily"
              :title="label('forecast7d')"
              :label="label"
            /><ForecastPanel
              :value="data.forecast_hourly"
              :title="label('forecast24h')"
              :label="label"
            />
          </div>
        </template>
        <template v-if="tab === 'own'"
          ><section v-if="localSubject" class="card space-y-4 p-5">
            <h2 class="font-semibold">{{ label('userBalances') }}</h2>
            <DataTable :items="userBalances" :empty="label('noData')" />
          </section><section class="card space-y-4 p-5">
            <button class="btn-secondary" :disabled="busy" @click="loadAudit">
              {{ label('audit') }}</button
            ><DataTable :items="audit" :empty="label('noData')" />
          </section>
          <div class="card space-y-4 p-5">
            <h2 class="font-semibold">{{ label('keys') }}</h2>
            <label
              >{{ label('adminKeys')
              }}<textarea v-model="adminKeys" rows="3" /></label
            ><button class="btn-primary" :disabled="busy" @click="saveKeys">
              {{ label('save') }}
            </button>
          </div>
          <section class="card space-y-4 p-5">
            <div class="flex justify-between">
              <h2 class="font-semibold">{{ label('subjects') }}</h2>
              <button class="btn-primary" @click="open('subject')">
                + {{ label('add') }}
              </button>
            </div>
            <div
              v-for="subject in subjects"
              :key="String(subject.id)"
              class="flex justify-between border-t py-3"
            >
              <span>{{ subject.name }}</span>
              <div class="flex gap-3">
                <button @click="open('subject', subject)">
                  {{ label('edit') }}</button
                ><button
                  class="text-red-500"
                  @click="remove('/own/subjects/' + subject.id)"
                >
                  {{ label('remove') }}
                </button>
              </div>
            </div>
          </section></template
        >
      </template>
      <template v-else-if="tab === 'settings'"
        ><form
          class="card grid gap-5 p-6 md:grid-cols-2"
          @submit.prevent="saveSettings"
        >
          <label v-for="(_, key) in settings" :key="key"
            >{{ label(String(key))
            }}<input
              v-if="booleanFields.has(String(key))"
              v-model="settings[key]"
              type="checkbox" /><input
              v-else
              v-model="settings[key]"
              :type="
                numberFields.has(String(key))
                  ? 'number'
                  : key === 'daily_report_time'
                    ? 'time'
                    : 'text'
              "
              required
          /></label>
          <div class="md:col-span-2">
            <p v-if="!encryptionConfigured" class="mb-3 text-amber-600">
              {{ label('disabledKey') }}
            </p>
            <button class="btn-primary" :disabled="busy">
              {{ label('save') }}
            </button>
          </div>
        </form></template
      >
      <template v-else-if="tab === 'notifications'"
        ><section class="card space-y-4 p-5">
          <div class="flex justify-between">
            <h2 class="font-semibold">{{ label('channels') }}</h2>
            <button class="btn-primary" @click="open('channel')">
              + {{ label('add') }}
            </button>
          </div>
          <div
            v-for="channel in channels"
            :key="String(channel.id)"
            class="flex flex-wrap justify-between gap-3 border-t py-3"
          >
            <span
              >{{ channel.name }}
              <small class="text-gray-500"
                >{{ channel.type }} ·
                {{ channel.has_secret ? '●' : '○' }}</small
              ></span
            >
            <div class="flex gap-3 text-sm">
              <button @click="open('channel', channel)">
                {{ label('edit') }}</button
              ><button
                @click="
                  run(async () => {
                    await write(
                      '/notifications/channels/' + channel.id + '/test',
                    )
                    notice = label('success')
                  })
                "
              >
                {{ label('test') }}</button
              ><button
                class="text-red-500"
                @click="remove('/notifications/channels/' + channel.id)"
              >
                {{ label('remove') }}
              </button>
            </div>
          </div>
        </section>
        <form class="card space-y-5 p-5" @submit.prevent="saveRules">
          <h2 class="font-semibold">{{ label('rules') }}</h2>
          <div
            v-for="event in Object.keys(rules)"
            :key="event"
            class="flex flex-wrap gap-4"
          >
            <strong class="w-20 text-sm">{{ event }}</strong
            ><label
              v-for="channel in channels"
              :key="String(channel.id)"
              class="flex items-center gap-2"
              ><input
                v-model="rules[event]"
                type="checkbox"
                :value="channel.id"
              />{{ channel.name }}</label
            >
          </div>
          <button class="btn-primary" :disabled="busy">
            {{ label('save') }}
          </button>
        </form>
        <h2 class="font-semibold">{{ label('deliveries') }}</h2>
        <DataTable :items="deliveries" :empty="label('noData')"
      /></template>
      <template v-else-if="tab === 'reports'"
        ><div class="card space-y-4 p-5">
          <p class="text-sm text-gray-500">{{ label('reportHint') }}</p>
          <div class="flex flex-wrap items-end gap-3">
            <label
              >{{ label('reports')
              }}<input v-model="reportDate" type="date" /></label
            ><button
              class="btn-secondary"
              :disabled="busy"
              @click="run(loadPanel)"
            >
              {{ label('preview') }}</button
            ><button class="btn-primary" :disabled="busy" @click="sendReport">
              {{ label('send') }}
            </button>
          </div>
          <pre
            class="max-h-[36rem] overflow-auto whitespace-pre-wrap rounded-lg bg-gray-50 p-5 text-sm dark:bg-gray-900"
            >{{
              data.body ??
              data.text ??
              data.markdown ??
              data.content ??
              JSON.stringify(data, null, 2)
            }}</pre
          >
        </div></template
      >
      <div
        v-if="modal"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
        @click.self="modal = ''"
      >
        <section
          role="dialog"
          aria-modal="true"
          :aria-label="label(editId === null ? 'add' : 'edit')"
          class="max-h-[90vh] w-full max-w-2xl overflow-auto rounded-2xl bg-white p-6 shadow-xl dark:bg-gray-800"
        >
          <div class="mb-5 flex justify-between">
            <h2 class="text-lg font-semibold">
              {{ label(editId === null ? 'add' : 'edit') }} ·
              {{
                label(
                  modal === 'station'
                    ? 'stations'
                    : modal === 'token'
                      ? 'tokens'
                      : modal === 'subject'
                        ? 'subjects'
                        : 'channels',
                )
              }}
            </h2>
            <button :aria-label="label('close')" @click="modal = ''">✕</button>
          </div>
          <p v-if="error" role="alert" class="mb-4 text-red-500">{{ error }}</p>
          <form class="grid gap-4 sm:grid-cols-2" @submit.prevent="save">
            <label v-for="key in fields" :key="key"
              >{{ label(key)
              }}<input
                v-if="booleanFields.has(key)"
                v-model="form[key]"
                :name="key"
                type="checkbox" /><select
                v-else-if="key === 'type'"
                v-model="form[key]"
                :name="key"
              >
                <option
                  v-for="value in modal === 'station'
                    ? stationTypes
                    : modal === 'subject'
                      ? ['local', 'newapi']
                      : channelTypes"
                  :key="value"
                >
                  {{ value }}
                </option></select
              ><select v-else-if="key === 'balance_mode'" v-model="form[key]">
                <option
                  v-for="value in ['shared', 'independent', 'quota']"
                  :key="value"
                >
                  {{ value }}
                </option></select
              ><input
                v-else
                v-model="form[key]"
                :name="key"
                :type="
                  numberFields.has(key)
                    ? 'number'
                    : key === 'url'
                      ? 'url'
                      : 'text'
                "
                :required="
                  ['name', 'cny_per_usd'].includes(key) ||
                  (key === 'url' && form.type !== 'fixed')
                "
                :placeholder="
                  key === 'cny_per_usd_override' ? label('inherit') : undefined
                " /></label
            ><label v-if="modal === 'station'" class="sm:col-span-2"
              >{{ label('channel_aliases')
              }}<input v-model="aliasText" /></label
            ><template v-if="modal === 'token'"
              ><p class="text-xs text-gray-500 sm:col-span-2">
                {{ label('rateHelp') }} {{ label('credentials') }}
              </p>
              <p
                v-if="!encryptionConfigured"
                class="text-amber-600 sm:col-span-2"
              >
                {{ label('disabledKey') }}
              </p>
              <label v-for="key in credentialFields" :key="key"
                >{{ key
                }}<input
                  v-model="secret[key]"
                  type="password"
                  autocomplete="new-password"
                  :disabled="!encryptionConfigured" /></label></template
            ><template v-if="modal === 'channel'">
              <label v-for="key in selectedChannelSchema.config" :key="key"
                >{{ key }}<input v-model="channelConfig[key]"
              /></label>
              <label v-for="key in selectedChannelSchema.secret" :key="key"
                >{{ key
                }}<input
                  v-model="secret[key]"
                  type="password"
                  autocomplete="new-password"
                  :disabled="!encryptionConfigured"
              /></label>
              <p class="text-xs text-gray-500 sm:col-span-2">
                {{ label('credentials') }}
              </p>
              <label class="sm:col-span-2"
                >{{ label('config')
                }}<textarea
                  v-model="jsonConfig"
                  rows="5"
                  spellcheck="false"
                /></label
              ><label class="sm:col-span-2"
                >{{ label('secret')
                }}<textarea
                  v-model="jsonSecret"
                  rows="3"
                  spellcheck="false"
                  :disabled="!encryptionConfigured"
                /></label
            ></template>
            <div class="mt-3 flex justify-end gap-3 sm:col-span-2">
              <button type="button" class="btn-secondary" @click="modal = ''">
                {{ label('cancel') }}</button
              ><button class="btn-primary" :disabled="busy">
                {{ label('save') }}
              </button>
            </div>
          </form>
        </section>
      </div>
    </div>
  </AppLayout>
</template>
<style scoped>
.relay-monitor {
  color: var(--color-text-primary, inherit);
}
label {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  font-size: 0.8125rem;
}
input:not([type='checkbox']),
select,
textarea {
  width: 100%;
  min-width: 8rem;
  padding: 0.6rem 0.75rem;
  border: 1px solid #d1d5db;
  border-radius: 0.65rem;
  background: transparent;
  color: inherit;
}
input[type='checkbox'] {
  width: 1rem;
  height: 1rem;
  accent-color: #6366f1;
}
button:disabled {
  opacity: 0.5;
  cursor: wait;
}
button:focus-visible,
input:focus-visible,
select:focus-visible,
textarea:focus-visible {
  outline: 2px solid #6366f1;
  outline-offset: 3px;
}
:global(.dark) input,
:global(.dark) select,
:global(.dark) textarea {
  border-color: #4b5563;
  color-scheme: dark;
}
</style>
