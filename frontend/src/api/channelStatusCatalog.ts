import { apiClient } from './client'
import { repeatedArrayParamsSerializer } from './channelMonitorV2'
import type { HealthState, MonitorCoverage, MonitorRange } from './channelMonitorV2'

/** Availability state of a catalog row; `unknown` means no data, never healthy. */
export type CatalogHealth = HealthState
export type CatalogBillingMode = 'token' | 'per_request' | 'image' | 'video'
export type CatalogFingerprintState = 'consistent' | 'suspected' | 'inconsistent' | 'not_checked'

export interface CatalogFingerprint {
  state: CatalogFingerprintState
  reason_code: string
  checked_at: string | null
  confidence: number | null
}

/** Why a bucket is `unknown`: nothing happened in it, or too little to judge. */
export type CatalogBucketReason = 'no_data' | 'low_sample'

export interface CatalogBucket {
  start: string
  health: CatalogHealth
  success_rate: number | null
  reason?: CatalogBucketReason
}

export interface CatalogModelRow {
  key: string
  name: string
  platform: string
  billing_mode: CatalogBillingMode
  /** Paid per-token input price (rate applied); null = not configured. */
  input_price: number | null
  /** Lowest paid per-unit price of non-token models; null = not configured. */
  per_request_price: number | null
  price_conflict: boolean
  health: CatalogHealth
  success_rate: number | null
  buckets: CatalogBucket[]
  event_count: number
  fingerprint: CatalogFingerprint
}

export interface CatalogGroupSummary {
  model_count: number
  health: CatalogHealth
  availability_rate: number | null
  degraded_count: number
  down_count: number
}

export interface CatalogGroup {
  id: number
  name: string
  platform: string
  rate_multiplier: number
  user_rate_multiplier: number | null
  peak_rate_enabled: boolean
  peak_start: string
  peak_end: string
  peak_rate_multiplier: number
  is_exclusive: boolean
  summary: CatalogGroupSummary
  models: CatalogModelRow[]
}

export interface ChannelStatusCatalog {
  groups: CatalogGroup[]
  coverage: MonitorCoverage | null
  capabilities: { fingerprint: boolean }
  /** Set when availability could not be loaded; prices stay valid. */
  status_error_code?: string
}

export interface CatalogBlock<T> {
  data: T | null
  error_code?: string
}

export interface CatalogOverview {
  health: CatalogHealth
  success_rate: number | null
  avg_response_ms: number | null
  event_count: number
  buckets: CatalogBucket[]
  fingerprint: CatalogFingerprint
}

export interface CatalogPaidPrices {
  input: number | null
  output: number | null
  cache_read: number | null
  cache_write: number | null
  cache_write_1h: number | null
}

export interface CatalogContextTier {
  label: string
  min_tokens: number
  max_tokens: number | null
  paid: CatalogPaidPrices
}

export interface CatalogRequestTier {
  label: string
  price: number | null
}

export interface CatalogTimePricing {
  timezone: string
  weekdays_only: boolean
  periods: Array<{ start_time: string; end_time: string; multiplier: number }>
}

export interface CatalogPricing {
  billing_mode: CatalogBillingMode
  rate: {
    group_rate: number
    user_rate: number | null
    effective_rate: number
    independent: boolean
  }
  paid: CatalogPaidPrices
  per_request: number | null
  image_input: number | null
  image_output: number | null
  request_tiers: CatalogRequestTier[] | null
  long_context: CatalogContextTier[] | null
  long_context_basis?: string
  long_context_enabled: boolean
  peak: { start: string; end: string; multiplier: number } | null
  time_pricing: CatalogTimePricing | null
  reasoning_effort_multipliers?: Record<string, number>
  official: CatalogPaidPrices | null
}

export interface CatalogIncident {
  start: string
  level: CatalogHealth
  duration_seconds: number
  min_success_rate: number | null
}

export interface CatalogEvents {
  total: number
  items: CatalogIncident[]
}

export interface CatalogDetails {
  key: string
  group_id: number
  group_name: string
  platform: string
  model: string
  overview: CatalogBlock<CatalogOverview>
  pricing: CatalogBlock<CatalogPricing>
  events: CatalogBlock<CatalogEvents>
}

export interface CatalogDetailsTarget {
  groupId: number
  platform: string
  model: string
}

// Go nil slices serialize as `null`; normalise so views can rely on arrays.
function normalizeCatalog(data: ChannelStatusCatalog): ChannelStatusCatalog {
  return {
    ...data,
    groups: (data.groups ?? []).map((group) => ({
      ...group,
      models: (group.models ?? []).map((row) => ({ ...row, buckets: row.buckets ?? [] })),
    })),
  }
}

function normalizeDetails(data: CatalogDetails): CatalogDetails {
  const overview = data.overview?.data
  const events = data.events?.data
  return {
    ...data,
    overview: overview ? { ...data.overview, data: { ...overview, buckets: overview.buckets ?? [] } } : data.overview,
    events: events ? { ...data.events, data: { ...events, items: events.items ?? [] } } : data.events,
  }
}

export async function getCatalog(range: MonitorRange, signal?: AbortSignal): Promise<ChannelStatusCatalog> {
  const { data } = await apiClient.get<ChannelStatusCatalog>('/channel-monitor-v2/catalog', {
    params: { range },
    paramsSerializer: { serialize: repeatedArrayParamsSerializer },
    signal,
  })
  return normalizeCatalog(data)
}

export async function getCatalogDetails(target: CatalogDetailsTarget, signal?: AbortSignal): Promise<CatalogDetails> {
  const { data } = await apiClient.get<CatalogDetails>('/channel-monitor-v2/catalog/details', {
    params: { group_id: target.groupId, platform: target.platform, model: target.model },
    paramsSerializer: { serialize: repeatedArrayParamsSerializer },
    signal,
  })
  return normalizeDetails(data)
}
