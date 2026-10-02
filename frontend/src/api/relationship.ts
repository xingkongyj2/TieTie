import { createInitialState } from '../data/mock'
import { readStorage, writeStorage } from '../lib/storage'
import type { AISettings, Member, MemberId, Message, RelationshipState, Reminder } from '../types'

const STORAGE_KEY = 'tietie.relationship.v1'
const MEMBER_IDS: MemberId[] = ['ai', 'self', 'partner']
const TONES: AISettings['tone'][] = ['warm', 'playful', 'concise']
const SETTING_FLAGS = ['weatherCare', 'anniversaryReminders'] as const

const delay = () => new Promise<void>((resolve) => setTimeout(resolve, 120 + Math.random() * 180))
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T
const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value)
const isMemberId = (value: unknown): value is MemberId => MEMBER_IDS.includes(value as MemberId)
const isTime = (value: unknown): value is string => typeof value === 'string' && /^([01]\d|2[0-3]):[0-5]\d$/.test(value)

function isDate(value: unknown, allowEmpty = false): value is string {
  if (typeof value !== 'string') return false
  if (allowEmpty && value === '') return true
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false
  const date = new Date(`${value}T12:00:00Z`)
  return !Number.isNaN(date.getTime()) && date.toISOString().slice(0, 10) === value
}

function isReminderTime(value: unknown): value is string {
  if (isTime(value)) return true
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) return false
  return isDate(value.slice(0, 10)) && isTime(value.slice(11))
}

function isMember(value: unknown): value is Member {
  return isRecord(value) && isMemberId(value.id)
    && typeof value.name === 'string' && value.name.trim().length > 0 && value.name.length <= 24
    && typeof value.role === 'string' && typeof value.avatar === 'string'
    && (value.gender === undefined || ['male', 'female', 'unspecified'].includes(value.gender as string))
    && isDate(value.birthday, true) && typeof value.bio === 'string' && value.bio.length <= 200
    && Array.isArray(value.hobbies) && value.hobbies.length <= 12
    && value.hobbies.every((item) => typeof item === 'string' && item.length <= 30)
}

function isSettings(value: unknown): value is AISettings {
  return isRecord(value) && typeof value.name === 'string'
    && value.name.trim().length > 0 && value.name.length <= 24
    && TONES.includes(value.tone as AISettings['tone'])
    && SETTING_FLAGS.every((key) => typeof value[key] === 'boolean')
}

function currentSettings(value: AISettings): AISettings {
  return { name: value.name, tone: value.tone, weatherCare: value.weatherCare, anniversaryReminders: value.anniversaryReminders }
}

function isReminder(value: unknown): value is Reminder {
  return isRecord(value) && typeof value.id === 'string' && value.id.length > 0
    && typeof value.title === 'string' && value.title.trim().length > 0 && value.title.length <= 500
    && (value.time === undefined || isReminderTime(value.time)) && (isMemberId(value.assignee) || value.assignee === 'both')
    && typeof value.completed === 'boolean'
}

function isMessage(value: unknown): value is Message {
  return isRecord(value) && typeof value.id === 'string' && isMemberId(value.sender)
    && typeof value.text === 'string' && isTime(value.time)
    && (value.kind === undefined || value.kind === 'text' || value.kind === 'reminder')
    && (value.reminderId === undefined || typeof value.reminderId === 'string')
}

function isState(value: unknown): value is RelationshipState {
  if (!isRecord(value) || !Array.isArray(value.members) || value.members.length !== 3
    || !value.members.every(isMember)) return false
  const members = value.members
  return MEMBER_IDS.every((id) => members.filter((member) => member.id === id).length === 1)
    && isSettings(value.settings) && Array.isArray(value.messages) && value.messages.every(isMessage)
    && Array.isArray(value.reminders) && value.reminders.every(isReminder) && isDate(value.togetherSince)
}

function loadState(): RelationshipState {
  const stored = readStorage<unknown>(STORAGE_KEY, createInitialState)
  if (isState(stored)) {
    // Retired avatar files must not leave broken images in profiles saved earlier.
    const defaults = createInitialState().members
    const settings = currentSettings(stored.settings)
    let changed = Object.keys(stored.settings).length !== Object.keys(settings).length
    const members = stored.members.map((member) => {
      if (member.avatar === '/avatars/bunny.png') {
        changed = true
        return { ...member, avatar: '/avatars/zodiac-rabbit.png' }
      }
      if (member.avatar === '/avatars/frog.png') {
        changed = true
        return { ...member, avatar: defaults.find((item) => item.id === member.id)?.avatar ?? '/avatars/ai-cat.png' }
      }
      return member
    })
    if (changed) {
      const migrated = { ...stored, members, settings }
      writeStorage(STORAGE_KEY, migrated)
      return migrated
    }
    return stored
  }
  // Reject old incompatible data as a whole instead of leaking invalid values into UI.
  const fallback = createInitialState()
  writeStorage(STORAGE_KEY, fallback)
  return fallback
}

function persist(state: RelationshipState): void {
  writeStorage(STORAGE_KEY, state)
}

let sequence = 0
function makeId(prefix: string): string {
  sequence += 1
  return `${prefix}-${Date.now().toString(36)}-${sequence.toString(36)}`
}

/**
 * Local-first prototype API. All writes use fresh persisted state after the mock
 * delay, keeping simultaneous independent edits from overwriting stale snapshots.
 * Real scheduled reminders and AI inference belong in a future backend adapter.
 */
export const relationshipApi = {
  async getState(): Promise<RelationshipState> {
    await delay()
    return clone(loadState())
  },

  async saveMember(member: Member): Promise<Member> {
    if (!isMember(member)) throw new Error('资料格式不正确，请检查昵称、生日和兴趣长度。')
    const updated = { ...clone(member), name: member.name.trim(), hobbies: member.hobbies.map((hobby) => hobby.trim()).filter(Boolean) }
    await delay()
    const state = loadState()
    state.members = state.members.map((item) => item.id === updated.id ? updated : item)
    if (updated.id === 'ai') state.settings.name = updated.name
    persist(state)
    return clone(updated)
  },

  async saveSettings(settings: AISettings): Promise<AISettings> {
    if (!isSettings(settings)) throw new Error('设置格式不正确，请检查昵称和语气选项。')
    const updated = { ...currentSettings(settings), name: settings.name.trim() }
    await delay()
    const state = loadState()
    state.settings = updated
    state.members = state.members.map((member) => member.id === 'ai' ? { ...member, name: state.settings.name } : member)
    persist(state)
    return clone(state.settings)
  },

  async addReminder(input: Omit<Reminder, 'id' | 'completed'>): Promise<Reminder> {
    const reminder: Reminder = { ...input, id: makeId('reminder'), completed: false }
    if (!isReminder(reminder)) throw new Error('请填写 500 字以内的提醒内容，并选择提醒对象。')
    if (reminder.time?.includes('T') && new Date(reminder.time).getTime() <= Date.now()) {
      throw new Error('这个时间已经过去啦，请选一个未来时间。')
    }
    await delay()
    const state = loadState()
    reminder.title = reminder.title.trim()
    state.reminders.push(reminder)
    persist(state)
    return clone(reminder)
  },

  async toggleReminder(id: string): Promise<Reminder> {
    await delay()
    const state = loadState()
    const reminder = state.reminders.find((item) => item.id === id)
    if (!reminder) throw new Error('这条提醒已经不在啦，请刷新后再试。')
    reminder.completed = !reminder.completed
    persist(state)
    return clone(reminder)
  },
}
