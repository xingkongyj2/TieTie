import type { CloudReminder } from '../types'

export type ReplyPhase = 'sending' | 'waiting' | 'thinking' | 'replying' | 'syncing' | 'delayed' | 'saved' | 'sent' | 'error' | 'complete'
export interface ReplyFeedback { phase: ReplyPhase; message?: string }

const reminderTime = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai', year: 'numeric', month: 'numeric', day: 'numeric',
  hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
})

/** Confirm only the verified reminder rows, including their actual scheduled time. */
export function savedReminderFeedback(reminders: CloudReminder[]): ReplyFeedback | null {
  if (!reminders.length) return null
  const details: string[] = []
  for (const reminder of reminders) {
    const date = new Date(reminder.dueAt)
    if (!reminder.id || !reminder.title.trim() || Number.isNaN(date.getTime())) return null
    const parts = new Map(reminderTime.formatToParts(date).map((part) => [part.type, part.value]))
    details.push(`${parts.get('year')}年${parts.get('month')}月${parts.get('day')}日 ${parts.get('hour')}:${parts.get('minute')} · ${reminder.title}`)
  }
  return { phase: 'saved', message: `已设置${reminders.length > 1 ? ` ${reminders.length} 条提醒` : ''}：${details.join('；')}。AI 回复稍后补上。` }
}

/** Cloud progress and read failures cannot undo a verified successful save. */
export function preserveSavedFeedback(current: ReplyFeedback | null, incoming: ReplyFeedback | null): ReplyFeedback | null {
  return current?.phase === 'saved' && incoming && incoming.phase !== 'complete' ? current : incoming
}
