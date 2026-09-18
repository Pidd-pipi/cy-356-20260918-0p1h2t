import { get, post } from '@/utils/request'
import type { UserInfo } from './auth'

export interface Plot {
  id: number
  name: string
  code: string
  area: number
  soil_type: string
  sunlight: string
  latitude: number
  longitude: number
  status: string
  adopter_id: number | null
  adopter: UserInfo | null
  description: string
  created_at: string
  // 释放入口感知：releasable=false 时 release_reason 说明缺少哪项前置条件。
  releasable: boolean
  release_reason_code: string
  release_reason: string
  has_completed_plan: boolean
  has_completed_harvest: boolean
}

export interface ReleaseEligibility {
  releasable: boolean
  reason_code: string
  reason: string
  has_plan: boolean
  completed_plans: number
  completed_plan_harvests: number
}

export interface PlotPayload {
  name: string
  code: string
  area: number
  soil_type: string
  sunlight: string
  latitude: number
  longitude: number
  description?: string
}

export function listPlots(params?: Record<string, any>): Promise<{ list: Plot[]; total: number; page: number; page_size: number }> {
  return get('/plots', { params })
}

export function getPlot(id: number): Promise<Plot> {
  return get(`/plots/${id}`)
}

export function createPlot(payload: PlotPayload): Promise<Plot> {
  return post('/plots', payload)
}

export function adoptPlot(id: number): Promise<Plot> {
  return post(`/plots/${id}/adopt`)
}

export function releasePlot(id: number): Promise<Plot> {
  return post(`/plots/${id}/release`)
}

// 查询地块释放条件（releasable=false 时 reason 说明缺少“已完成计划”或“收成记录”哪一项）。
export function getReleaseEligibility(id: number): Promise<ReleaseEligibility> {
  return get(`/plots/${id}/release-eligibility`)
}
