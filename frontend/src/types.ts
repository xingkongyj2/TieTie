/** Shared domain models: intentionally independent of React and browser APIs. */
export type MemberId = 'ai' | 'self' | 'partner'

export interface RegionLocation {
  provinceCode: string
  province: string
  cityCode: string
  city: string
  districtCode: string
  district: string
  codeSystem: string
}

export interface Member {
  id: MemberId
  /** Verified account identity for a member of the shared cloud space. */
  userId?: number
  name: string
  role: string
  avatar: string
  gender?: 'male' | 'female' | 'unspecified'
  birthday: string
  region?: RegionLocation
  hobbies: string[]
  bio: string
}

export interface AISettings {
  name: string
  tone: 'warm' | 'playful' | 'concise'
  weatherCare: boolean
  anniversaryReminders: boolean
}

export interface Reminder {
  id: string
  title: string
  /** Older scheduled entries may have a local datetime or HH:mm. */
  time?: string
  assignee: MemberId | 'both'
  completed: boolean
  status?: CloudReminderStatus
  recipientIds?: number[]
  taskStatus?: 'pending' | 'running' | 'completed' | 'cancelled' | 'failed' | 'uncertain'
  taskCompletedAt?: string
  deliveredAt?: string
}

export type CloudReminderStatus = 'scheduled' | 'dispatching' | 'delivered' | 'completed' | 'cancelled' | 'failed' | 'uncertain'

export interface CloudMember {
  userId: number
  name: string
}

export interface CloudReminder {
  id: string
  sessionId: string
  title: string
  dueAt: string
  recipientIds: number[]
  createdBy: number
  status: CloudReminderStatus
  taskStatus?: 'pending' | 'running' | 'completed' | 'cancelled' | 'failed' | 'uncertain'
  taskCompletedAt?: string
  deliveredAt?: string
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

export type MessageVisibility = 'shared' | 'private'

export interface Message {
	weatherCards?: import('./api/care').WeatherCardData[]
  replyMode?: 'silent'
  visibility?: MessageVisibility
  id: string
  sender: MemberId | 'user'
  userId?: number
  displayName?: string
  recipientIds?: number[]
  source?: 'chat' | 'reminder'
  text: string
  time: string
  /** Cloud event timestamp, used for local-time display and day separators. */
  createdAt?: string
  kind?: 'text' | 'reminder' | 'ask'
  reminderId?: string
  reminderIds?: string[]
  reminderError?: string
  /** Legacy display flag; shared conversation protocol renders final events only. */
  streaming?: boolean
  images?: string[]
  /** Original attachment names; paths and extraction instructions stay internal. */
  files?: string[]
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
