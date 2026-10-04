import type { Message } from '../types'

// Unknown legacy authors and internal event types never become chat bubbles.
// The backend is authoritative; this also protects old cached client state.
export function isVisibleChatMessage(message: Message, members: readonly { userId?: number }[]): boolean {
  if (message.kind !== undefined && !['text', 'reminder', 'ask'].includes(message.kind)) return false
  if (message.source !== undefined && !['chat', 'reminder', 'reminder_update'].includes(message.source)) return false
  if (message.sender === 'ai') return true
  return (message.sender === 'self' || message.sender === 'partner')
    && typeof message.userId === 'number' && message.userId > 0
    && members.some((member) => member.userId === message.userId)
}
