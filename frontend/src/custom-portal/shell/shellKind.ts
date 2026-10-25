/** `pending` renders nothing until an admin's custom-page ownership is known. */
export type ShellKind = 'portal' | 'upstream' | 'pending'

/** Setting this localStorage key to `upstream` restores the upstream sidebar shell. */
export const SHELL_OVERRIDE_STORAGE_KEY = 'sub2api.portal.shell'

export interface ShellContext {
  path: string
  routeName?: string | symbol | null | undefined
  customPageId?: string
  isAdmin: boolean
  isSimpleMode: boolean
  adminCustomPageIds: readonly string[]
  /** Admin settings (which hold admin-only custom pages) are still loading. */
  adminCustomPagesPending?: boolean
  override?: string | null
}

/**
 * Picks the layout for a page. Admin-owned pages keep the upstream sidebar
 * until the admin archive shell lands; the ownership exceptions match where
 * upstream `AppSidebar` lists them: `/keys` for admins in simple mode and
 * custom pages whose visibility is `admin`.
 */
export function resolveShellKind(ctx: ShellContext): ShellKind {
  if (ctx.override === 'upstream') return 'upstream'
  if (ctx.path === '/admin' || ctx.path.startsWith('/admin/')) return 'upstream'
  if (ctx.isAdmin && ctx.isSimpleMode && ctx.path === '/keys') return 'upstream'
  if (ctx.isAdmin && ctx.routeName === 'CustomPage' && ctx.customPageId) {
    if (ctx.adminCustomPageIds.includes(ctx.customPageId)) return 'upstream'
    // A failed load falls back to the portal rather than blanking the page.
    if (ctx.adminCustomPagesPending) return 'pending'
  }
  return 'portal'
}

export function readShellOverride(storage: Pick<Storage, 'getItem'> | undefined): string | null {
  try {
    return storage?.getItem(SHELL_OVERRIDE_STORAGE_KEY) ?? null
  } catch {
    return null
  }
}
