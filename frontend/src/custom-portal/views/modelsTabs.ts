import type { LocationQuery } from 'vue-router'
import type { ModelPlazaResponse } from '@/api/modelPlaza'

/** Tabs of the models page: upstream channel status, and the model plaza pricing moved in. */
export type ModelsTab = 'status' | 'pricing'

export const MODELS_TABS: readonly ModelsTab[] = ['status', 'pricing']

/**
 * `view=pricing` opens pricing; anything else is the status tab, because the
 * upstream status page rewrites the whole query and drops `view` on its own.
 */
export function resolveModelsTab(query: LocationQuery, visible: readonly ModelsTab[]): ModelsTab | null {
  const requested: ModelsTab = query.view === 'pricing' ? 'pricing' : 'status'
  return visible.includes(requested) ? requested : (visible[0] ?? null)
}

/** Keeps the status filters in the query while switching tabs. */
export function modelsTabQuery(query: LocationQuery, tab: ModelsTab): LocationQuery {
  const { view: _view, ...rest } = query
  return tab === 'pricing' ? { ...rest, view: 'pricing' } : rest
}

/**
 * The plaza lists groups as a showcase, including subscription groups the user
 * has not subscribed to. The console shows only groups the user can bind a key
 * to, the same set `/groups/available` returns.
 */
export function openedPlazaGroups(response: ModelPlazaResponse, openedIds: ReadonlySet<number>): ModelPlazaResponse {
  return { ...response, groups: response.groups.filter((group) => openedIds.has(group.id)) }
}
