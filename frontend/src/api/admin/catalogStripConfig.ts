/**
 * Admin strip rules of the "Models & Channels" page: how availability blocks
 * are counted (by time or by recent calls) and the colour / height steps.
 */

import { apiClient } from '../client'
import type { CatalogStripConfig } from '@/api/channelStatusCatalog'

export async function get(): Promise<CatalogStripConfig> {
  const { data } = await apiClient.get<CatalogStripConfig>('/admin/catalog-strip-config')
  return data
}

export async function update(config: CatalogStripConfig): Promise<CatalogStripConfig> {
  const { data } = await apiClient.put<CatalogStripConfig>('/admin/catalog-strip-config', config)
  return data
}
