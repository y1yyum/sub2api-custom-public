import type { MonitorFilter, MonitorMatrixRow } from '@/api/channelMonitorV2'

/** Backend label for models outside the operator's per-platform allow-list. */
export const MONITOR_OTHER_MODEL = '__other__'

export const MODEL_STATUS_RANGE = '24h'

export type ModelStatus =
  | { kind: 'row'; row: MonitorMatrixRow }
  | { kind: 'other' }
  | { kind: 'none' }

export type ModelStatusCellState = ModelStatus | { kind: 'loading' } | { kind: 'failed' }

export interface ModelStatusTarget {
  groupId: number
  groupName: string
  platform: string
  model: string
}

/** The backend also sends `success_rate`; the upstream `MonitorMetric` type omits it. */
type RateMetric = MonitorMatrixRow['metrics'] & { success_rate?: number }

/**
 * User-facing matrix rows have absolute counts zeroed by the backend, so traffic
 * is read from the rates: both are 0 only when the row saw no requests.
 */
export function hasMonitorTraffic(row: MonitorMatrixRow) {
  const { request_count, error_rate, success_rate = 0 } = row.metrics as RateMetric
  return request_count > 0 || success_rate + error_rate > 0
}

function rowKey(groupId: number, platform: string, model: string) {
  return `${groupId}\u0000${platform}\u0000${model}`
}

/**
 * Joins plaza rows to `platform_group_model` matrix rows on (group, platform, model).
 * A named row without requests is still tracked on its own, so it reads as no data.
 * A model with no row of its own counts as "not tracked separately" only when the
 * same group and platform report traffic in an `__other__` row.
 */
export function indexModelStatus(rows: MonitorMatrixRow[]) {
  const named = new Map<string, MonitorMatrixRow>()
  const collapsed = new Set<string>()
  for (const row of rows) {
    if (row.group_id == null || !row.model) continue
    if (row.model !== MONITOR_OTHER_MODEL) named.set(rowKey(row.group_id, row.platform, row.model), row)
    else if (hasMonitorTraffic(row)) collapsed.add(rowKey(row.group_id, row.platform, ''))
  }
  return (groupId: number, platform: string, model: string): ModelStatus => {
    const row = named.get(rowKey(groupId, platform, model))
    if (row) return hasMonitorTraffic(row) ? { kind: 'row', row } : { kind: 'none' }
    return collapsed.has(rowKey(groupId, platform, '')) ? { kind: 'other' } : { kind: 'none' }
  }
}

export function modelStatusFilter(target: ModelStatusTarget): MonitorFilter {
  return { range: MODEL_STATUS_RANGE, platforms: [target.platform], groupIds: [target.groupId], models: [target.model] }
}
