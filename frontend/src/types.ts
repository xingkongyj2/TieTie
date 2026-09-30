/** Shared domain models: intentionally independent of React and browser APIs. */
export type MemberId = 'ai' | 'self' | 'partner'

export interface Member {
  id: MemberId
  name: string
  role: string
  avatar: string
  birthday: string
  hobbies: string[]
  bio: string
}

export interface AISettings {
  name: string
  tone: 'warm' | 'playful' | 'concise'
  sharedReminders: boolean
  weatherCare: boolean
  anniversaryReminders: boolean
  quietHours: boolean
}

export interface Reminder {
  id: string
  title: string
  /** Local datetime YYYY-MM-DDTHH:mm, or HH:mm for the original demo fixture. */
  time: string
  assignee: MemberId | 'both'
  completed: boolean
}

export interface Message {
  id: string
  sender: MemberId
  text: string
  time: string
  /** Cloud event timestamp, used for local-time display and day separators. */
  createdAt?: string
  kind?: 'text' | 'reminder'
  reminderId?: string
  /** Temporary text assembled from SSE deltas until the buffered event arrives. */
  streaming?: boolean
  images?: string[]
}

export interface RelationshipState {
  members: Member[]
  settings: AISettings
  messages: Message[]
  reminders: Reminder[]
  togetherSince: string
}
