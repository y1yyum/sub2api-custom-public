/**
 * Admin intelligence question bank and scheduled-test helpers.
 * Questions are maintained by admins; quiz plans pick from them.
 */

import { apiClient } from '../client'
import type {
  IntelligenceGradeRequest,
  IntelligenceQuestion,
  IntelligenceQuestionRequest,
  ScheduledTestPlan
} from '@/types'

/** Quiz runs are pass, fail or error; char art runs are success or error. */
export type IntelligenceRunOutcome = 'pass' | 'fail' | 'success' | 'error'

export interface IntelligencePlanOverview extends ScheduledTestPlan {
  account_name: string
  account_platform: string
  /** Up to 10 recent runs, oldest first. */
  recent: { checked_at: string; outcome: IntelligenceRunOutcome }[]
}

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

/** Char art and quiz plans of every account, newest first. */
export async function listPlans(): Promise<IntelligencePlanOverview[]> {
  const { data } = await apiClient.get<IntelligencePlanOverview[]>('/admin/intelligence-plans')
  return data ?? []
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

export const intelligenceQuestionsAPI = { list, create, update, remove, grade, listPlans, mappedModels }

export default intelligenceQuestionsAPI
