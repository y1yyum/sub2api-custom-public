/** Table pages that span the full window width instead of the 1296px content column. */
export const WIDE_TABLE_PATHS: readonly string[] = ['/keys', '/usage', '/available-channels']

export function isWideTablePath(path: string): boolean {
  return WIDE_TABLE_PATHS.includes(path)
}
