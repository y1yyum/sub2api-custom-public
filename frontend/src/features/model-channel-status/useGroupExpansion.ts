import { computed, ref, watch, type Ref } from 'vue'
import type { CatalogGroup } from '@/api/channelStatusCatalog'
import { defaultExpandedIds } from './catalogFormat'

export const GROUP_EXPANSION_STORAGE_KEY = 'model-channel-status:expanded-groups'

function readStored(): number[] | null {
  try {
    const raw = localStorage.getItem(GROUP_EXPANSION_STORAGE_KEY)
    if (raw == null) return null
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return null
    return parsed.filter((id): id is number => Number.isInteger(id))
  } catch {
    return null
  }
}

function writeStored(ids: number[]) {
  try {
    localStorage.setItem(GROUP_EXPANSION_STORAGE_KEY, JSON.stringify(ids))
  } catch {
    /* storage unavailable: expansion simply is not persisted */
  }
}

/**
 * Expanded-group state keyed by group id. Without a stored choice the default
 * rule applies once data arrives; afterwards the user's toggles win and ids of
 * groups that no longer exist are dropped.
 */
export function useGroupExpansion(groups: Ref<CatalogGroup[]>) {
  const stored = readStored()
  const expanded = ref(new Set<number>(stored ?? []))
  const customised = ref(stored != null)

  watch(
    groups,
    (list) => {
      if (list.length === 0) return
      if (!customised.value) {
        expanded.value = new Set(defaultExpandedIds(list))
        return
      }
      const known = new Set(list.map((g) => g.id))
      const pruned = [...expanded.value].filter((id) => known.has(id))
      if (pruned.length !== expanded.value.size) {
        expanded.value = new Set(pruned)
        writeStored(pruned)
      }
    },
    { immediate: true },
  )

  function commit(next: Set<number>) {
    expanded.value = next
    customised.value = true
    writeStored([...next])
  }

  function isExpanded(id: number): boolean {
    return expanded.value.has(id)
  }

  function toggle(id: number) {
    const next = new Set(expanded.value)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    commit(next)
  }

  function expandAll() {
    commit(new Set(groups.value.map((g) => g.id)))
  }

  function collapseAll() {
    commit(new Set())
  }

  const allExpanded = computed(
    () => groups.value.length > 0 && groups.value.every((g) => expanded.value.has(g.id)),
  )

  return { isExpanded, toggle, expandAll, collapseAll, allExpanded }
}
