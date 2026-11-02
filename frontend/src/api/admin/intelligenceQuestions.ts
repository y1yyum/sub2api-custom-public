/**
 * Admin intelligence question bank and scheduled-test helpers.
 * Questions are maintained by admins; quiz plans pick from them.
 */

import { apiClient } from '../client'
import type {
  IntelligenceGradeRequest,
  IntelligenceQuestion,
  IntelligenceQuestionRequest
} from '@/types'

export async function list(): Promise<IntelligenceQuestion[]> {
  const { data } = await apiClient.get<IntelligenceQuestion[]>('/admin/intelligence-questions')
  return data ?? []
}

export async function create(req: IntelligenceQuestionRequest): Promise<IntelligenceQuestion> {
  const { data } = await apiClient.post<IntelligenceQuestion>('/admin/intelligence-questions', req)
  return data
}

export async function update(id: number, req: IntelligenceQuestionRequest): Promise<IntelligenceQuestion> {
  const { data } = await apiClient.put<IntelligenceQuestion>(`/admin/intelligence-questions/${id}`, req)
  return data
}

export async function remove(id: number): Promise<void> {
  await apiClient.delete(`/admin/intelligence-questions/${id}`)
}

export async function grade(req: IntelligenceGradeRequest): Promise<boolean> {
  const { data } = await apiClient.post<{ passed: boolean }>('/admin/intelligence-questions/grade', req)
  return Boolean(data?.passed)
}

/** Maps public model IDs to the account's upstream names (account model mapping applied). */
export async function mappedModels(accountId: number, models: string[]): Promise<Record<string, string>> {
  if (models.length === 0) return {}
  const { data } = await apiClient.get<Record<string, string>>(`/admin/accounts/${accountId}/mapped-models`, {
    params: { model: models },
    paramsSerializer: { indexes: null }
  })
  return data ?? {}
}

export const intelligenceQuestionsAPI = { list, create, update, remove, grade, mappedModels }

export default intelligenceQuestionsAPI
