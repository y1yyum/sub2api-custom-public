import { apiClient } from '@/api/client'
import type { RecordData } from './model'
const base = '/admin/relay-monitor'
export async function read<T = RecordData>(
  path: string,
  params?: RecordData,
): Promise<T> {
  return (await apiClient.get<T>(base + path, { params })).data
}
export async function write(
  path: string,
  body: RecordData = {},
  method: 'post' | 'put' | 'delete' = 'post',
): Promise<unknown> {
  if (method === 'delete') return (await apiClient.delete(base + path)).data
  return (await apiClient[method](base + path, body)).data
}
