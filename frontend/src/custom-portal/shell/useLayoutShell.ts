import { computed, inject, provide, type ComputedRef, type InjectionKey } from 'vue'
import { useRoute } from 'vue-router'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import { useAuthStore } from '@/stores/auth'
import { readShellOverride, resolveShellKind, type ShellKind } from './shellKind'

const INSIDE_SHELL: InjectionKey<boolean> = Symbol('portal-layout-shell')

export interface LayoutShell {
  /** Already rendered inside a shell: render the slot only. */
  nested: boolean
  kind: ComputedRef<ShellKind>
  /** Upstream starts its onboarding tour from the layout; the portal shell pauses it. */
  runsOnboarding: boolean
}

/** Replacement hook called from upstream `AppLayout.vue`. */
export function useLayoutShell(): LayoutShell {
  const nested = inject(INSIDE_SHELL, false)
  provide(INSIDE_SHELL, true)

  const route = useRoute()
  const authStore = useAuthStore()
  const adminSettingsStore = useAdminSettingsStore()
  const override = readShellOverride(typeof window === 'undefined' ? undefined : window.localStorage)

  if (!nested && authStore.isAdmin) {
    void adminSettingsStore.fetch()
  }

  const kind = computed(() =>
    resolveShellKind({
      path: route.path,
      routeName: route.name,
      customPageId: typeof route.params.id === 'string' ? route.params.id : undefined,
      isAdmin: authStore.isAdmin,
      isSimpleMode: authStore.isSimpleMode,
      adminCustomPageIds: adminSettingsStore.customMenuItems
        .filter((item) => item.visibility === 'admin')
        .map((item) => item.id),
      adminCustomPagesPending: !adminSettingsStore.loaded && adminSettingsStore.loading,
      override
    })
  )

  return { nested, kind, runsOnboarding: !nested && kind.value === 'upstream' }
}
