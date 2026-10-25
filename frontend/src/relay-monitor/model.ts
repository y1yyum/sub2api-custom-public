export type RecordData = Record<string, unknown>
export interface Station extends RecordData {
  id: number
  name: string
  url: string
  type: string
  enabled: boolean
  cny_per_usd: string
  version: number
  balance_mode: string
  balance_token_id: number | null
}
export interface Token extends RecordData {
  id: number
  name: string
  enabled: boolean
  has_credential: boolean
  cny_per_usd_override: string | null
  effective_cny_per_usd: string
  rate_source: string
  version: number
}
export function effectiveRate(
  station: string,
  override: string | null,
): string {
  return override ?? station
}
function validateRate(value: unknown): void {
  if (
    typeof value !== 'string' ||
    !/^\d+(\.\d+)?$/.test(value) ||
    !/[1-9]/.test(value)
  )
    throw new Error('Rate must be a positive decimal')
}
export function tokenPayload(input: RecordData): RecordData {
  const result: RecordData = {
    ...input,
    cny_per_usd_override: input.cny_per_usd_override || null,
  }
  if (result.cny_per_usd_override !== null)
    validateRate(result.cny_per_usd_override)
  const credential = Object.fromEntries(
    Object.entries((input.credential || {}) as Record<string, string>).filter(
      ([, value]) => value !== '',
    ),
  )
  delete result.credential
  if (Object.keys(credential).length) result.credential = credential
  return result
}
export function stationPayload(input: RecordData): RecordData {
  validateRate(input.cny_per_usd)
  const result = { ...input }
  delete result.credential
  return result
}
export function displayMoney(value: unknown): string {
  return value === null || value === undefined ? '—' : String(value)
}
export function trendRows(value: unknown): RecordData[] {
  if (Array.isArray(value)) return value as RecordData[]
  if (value && typeof value === 'object')
    return Object.entries(value)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([date, cost_cny]) => ({ date, cost_cny }))
  return []
}
export function rows(value: unknown, key: string): RecordData[] {
  if (Array.isArray(value)) return value as RecordData[]
  if (value && typeof value === 'object') {
    const items = (value as RecordData)[key]
    if (Array.isArray(items)) return items as RecordData[]
  }
  return []
}
