import { getModelPlaza, type ModelPlazaGroup, type ModelPlazaResponse, type PlazaModel } from '@/api/modelPlaza'
import { BILLING_MODE_IMAGE, BILLING_MODE_TOKEN, BILLING_MODE_VIDEO } from '@/constants/channel'

export const HOMEPAGE_MODELS_TIMEOUT_MS = 5000
export const HOMEPAGE_MODELS_MAX_ROWS = 6

/**
 * Whether this visitor may read the plaza: `pending` until the public settings
 * load. Requesting a closed plaza is not harmless: a 401 makes the upstream API
 * client clear the session and redirect to the login page.
 */
export type PlazaAccess = 'pending' | 'open' | 'closed'

/** Paid prices in USD after the group rate: per token, or per image / request. */
export type HomepageModelPrice =
  | { kind: 'token'; input: number | null; output: number | null }
  | { kind: 'image' | 'request'; unit: number }

export interface HomepageModel {
  id: string
  name: string
  platform: string
  price: HomepageModelPrice
}

interface Candidate extends HomepageModel {
  officialOutput: number | null
}

function groupRate(group: ModelPlazaGroup): number {
  return group.user_rate_multiplier ?? group.rate_multiplier
}

/** Same rate rules as the plaza pricing table, standard hours only. */
function unitRate(group: ModelPlazaGroup, mode: string): number {
  if (mode === BILLING_MODE_VIDEO && group.video_rate_independent) return Math.max(0, group.video_rate_multiplier ?? 1)
  if (mode === BILLING_MODE_IMAGE && group.image_rate_independent) return group.image_rate_multiplier ?? 1
  return groupRate(group)
}

function scaled(value: number | null | undefined, rate: number): number | null {
  return value == null ? null : value * rate
}

function lowestRequestPrice(model: PlazaModel): number | null {
  const pricing = model.pricing!
  if (pricing.per_request_price != null) return pricing.per_request_price
  const tiers = (pricing.intervals ?? []).map((tier) => tier.per_request_price).filter((v): v is number => v != null)
  return tiers.length ? Math.min(...tiers) : null
}

function priceOf(group: ModelPlazaGroup, model: PlazaModel): HomepageModelPrice | null {
  if (!model.pricing) return null
  const mode = model.pricing.billing_mode || BILLING_MODE_TOKEN
  if (mode === BILLING_MODE_TOKEN) {
    const rate = groupRate(group)
    const input = scaled(model.pricing.input_price, rate)
    const output = scaled(model.pricing.output_price, rate)
    return input == null && output == null ? null : { kind: 'token', input, output }
  }
  const unit = scaled(lowestRequestPrice(model), unitRate(group, mode))
  return unit == null ? null : { kind: mode === BILLING_MODE_IMAGE ? 'image' : 'request', unit }
}

function comparable(price: HomepageModelPrice): number {
  return price.kind === 'token' ? (price.output ?? price.input ?? Infinity) : price.unit
}

/** Token prices win over unit prices, so the row never depends on group order. */
function cheaper(next: HomepageModelPrice, current: HomepageModelPrice): boolean {
  if (next.kind !== current.kind) return next.kind === 'token'
  return comparable(next) < comparable(current)
}

/**
 * Lowest paid price per model across standard, non-exclusive groups. Subscription
 * groups draw down a quota instead of charging per token, so their rates would
 * understate what a pay-as-you-go caller pays. Ordered like the plaza table:
 * token models first, then by official output price, newest name first.
 */
export function summarizeHomepageModels(response: ModelPlazaResponse, max = HOMEPAGE_MODELS_MAX_ROWS): HomepageModel[] {
  const best = new Map<string, Candidate>()
  for (const group of response.groups ?? []) {
    if (group.is_exclusive || group.subscription_type !== 'standard') continue
    for (const model of group.models ?? []) {
      const price = priceOf(group, model)
      if (!price) continue
      const platform = model.platform || group.platform || 'unknown'
      const id = `${platform}:${model.name}`
      const current = best.get(id)
      if (current && !cheaper(price, current.price)) continue
      best.set(id, { id, name: model.name, platform, price, officialOutput: model.official_pricing?.output_price ?? null })
    }
  }
  return [...best.values()]
    .sort((a, b) => {
      const ta = a.price.kind === 'token'
      const tb = b.price.kind === 'token'
      if (ta !== tb) return ta ? -1 : 1
      if (a.officialOutput !== b.officialOutput) {
        if (a.officialOutput == null) return 1
        if (b.officialOutput == null) return -1
        return b.officialOutput - a.officialOutput
      }
      return b.name.localeCompare(a.name)
    })
    .slice(0, max)
    .map(({ officialOutput: _officialOutput, ...model }) => model)
}

export async function fetchHomepageModels(signal?: AbortSignal): Promise<HomepageModel[]> {
  return summarizeHomepageModels(await getModelPlaza({ signal }))
}
