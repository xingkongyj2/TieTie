/** Shared domain models: intentionally independent of React and browser APIs. */
export type MemberId = 'ai' | 'self' | 'partner'

export interface Member {
  id: MemberId
  name: string
  role: string
  avatar: string
  gender?: 'male' | 'female' | 'unspecified'
  birthday: string
  hobbies: string[]
  bio: string
}

export interface AISettings {
  name: string
  tone: 'warm' | 'playful' | 'concise'
  weatherCare: boolean
  anniversaryReminders: boolean
  quietHours: boolean
}

export interface Reminder {
  id: string
  title: string
  /** Older scheduled entries may have a local datetime or HH:mm. */
  time?: string
  assignee: MemberId | 'both'
  completed: boolean
}

/** Agent 通过云端自定义工具（AskUserQuestion）抛出的选择题。 */
export interface AskOption {
  label: string
  description?: string
}

export interface AskQuestion {
  header?: string
  question: string
  multiSelect?: boolean
  options?: AskOption[]
}

export interface Message {
  id: string
  sender: MemberId
  text: string
  time: string
  /** Cloud event timestamp, used for local-time display and day separators. */
  createdAt?: string
  kind?: 'text' | 'reminder' | 'ask'
  reminderId?: string
  /** Temporary text assembled from SSE deltas until the buffered event arrives. */
  streaming?: boolean
  images?: string[]
  /** kind === 'ask'：题目内容；answered 表示云端已收到本条工具应答。 */
  ask?: AskQuestion[]
  answered?: boolean
}

export interface RelationshipState {
  members: Member[]
  settings: AISettings
  messages: Message[]
  reminders: Reminder[]
  togetherSince: string
}
