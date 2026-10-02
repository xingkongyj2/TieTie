import { request } from './client'
import type { RegionLocation } from '../types'

export interface WeatherHour {
  humidity?: number | null; uv?: number | null; time: string; temperature: number | null; feelsLike: number | null; rainChance: number | null
  wind: number | null; visibility: number | null; pm25: number | null; aqi: number | null; code: number | null
}
export interface WeatherView { recipientIds: number[]; recipientNames: string[]; metrics: string[]; summary?: string[]; comparisons: { metric: string; text: string }[]; alerts: string[]; clothing?: string; localAdvice?: string; preferenceHint: string }
export interface WeatherCardData {
 aqiLabel?: string; attributions?: string[]; views?: WeatherView[]; currentAir?: { retrievedAt: string; pm25: number | null; aqi: number | null; aqiLabel: string }; airComparison?: string
  mode: 'morning' | 'night'; region: RegionLocation; precision: 'district' | 'city'
  recipientIds: number[]; recipientNames: string[]
  day: { date: string; min: number; max: number; code: number; rainChance: number | null; wind: number | null; uv: number | null }
  description: string; comparison: string; alerts: string[]; clothing: string; hours: WeatherHour[]
  reminders: { title: string; time: string; recipientIds: number[] }[]; moreReminders: number
  airAvailable: boolean; generatedAt: string; source: string
}
export interface CareMode { mode: 'morning' | 'night'; enabled: boolean; time: string; nextDue: string; state: string; lastError?: string }
export interface CareState {
  modes: CareMode[]
  members: { userId: number; name: string; region: RegionLocation }[]
  reports: { id: string; mode: string; cards: WeatherCardData[]; createdAt: string }[]
}
const base = (session: string) => `/api/qoder/sessions/${encodeURIComponent(session)}`
export const careApi = {
  get: (session: string): Promise<CareState> => request(`${base(session)}/care-settings`),
  save: (session: string, mode: CareMode): Promise<CareState> => request(`${base(session)}/care-settings`, { method: 'PUT', body: { mode: mode.mode, enabled: mode.enabled, time: mode.time } }),
  preview: (session: string, mode: 'morning' | 'night'): Promise<{ cards: WeatherCardData[] }> => request(`${base(session)}/care-preview?mode=${mode}`),
}
