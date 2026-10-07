import type { Message } from '../types'

export const chatMessageKey = (message: Message): string => message.renderKey ?? message.id
export const chatMessageAnchor = (message: Message): string => `chat-message-${chatMessageKey(message)}`
export type ChatScrollTarget = { kind: 'reply'; key: string } | { kind: 'bottom' }

/** Queue a single scroll for new content; history/feedback refreshes are read-only. */
export class ChatScrollPolicy {
  private owner = ''
  private initialized = false
  private previewed = false
  private seen = new Set<string>()
  private completed = new Set<string>()
  pending: ChatScrollTarget | null = null

  observe(owner: string, messages: Message[], loaded: boolean, followBottom: boolean): void {
    if (owner !== this.owner) {
      this.owner = owner
      this.initialized = false
      this.previewed = false
      this.seen.clear()
      this.completed.clear()
      this.pending = null
    }
    if (!loaded) {
      if (!this.initialized && !this.previewed && messages.length) {
        this.previewed = true
        if (followBottom && !this.pending) this.pending = { kind: 'bottom' }
      }
      return
    }
    if (!this.initialized) {
      this.initialized = true
      if (messages.length && followBottom && !this.pending) this.pending = { kind: 'bottom' }
      for (const message of messages) {
        const key = chatMessageKey(message)
        this.seen.add(key)
        if (message.sender === 'ai' && !message.streaming) this.completed.add(key)
      }
      return
    } else {
      // A full history refresh can backfill older rows before the known tail.
      // Only appended replies or a previously streaming row can finish a turn.
      let lastKnownIndex = -1
      messages.forEach((message, index) => { if (this.seen.has(chatMessageKey(message))) lastKnownIndex = index })
      const reply = messages.find((message, index) => message.sender === 'ai' && !message.streaming
        && !this.completed.has(chatMessageKey(message))
        && (index > lastKnownIndex || this.seen.has(chatMessageKey(message))))
      if (reply) this.pending = { kind: 'reply', key: chatMessageKey(reply) }
      else if (!this.pending && followBottom && messages.some((message) => message.sender !== 'ai' && !this.seen.has(chatMessageKey(message)))) {
        this.pending = { kind: 'bottom' }
      }
    }
    for (const message of messages) {
      const key = chatMessageKey(message)
      this.seen.add(key)
      if (message.sender === 'ai' && !message.streaming) this.completed.add(key)
    }
  }

  requestBottom(): void { this.pending = { kind: 'bottom' } }
  cancelBottom(): void { if (this.pending?.kind === 'bottom') this.pending = null }
  complete(target: ChatScrollTarget): void { if (this.pending === target) this.pending = null }
}

/** Native query coordinates are viewport-relative, while scrollTop is content-relative. */
export function chatTargetScrollTop(kind: ChatScrollTarget['kind'], currentTop: number,
  viewport: { top: number; height: number }, target: { top: number }, bottom: { top: number; height: number }): number {
  const maximum = Math.max(0, currentTop + bottom.top + bottom.height - viewport.top - viewport.height)
  return kind === 'bottom' ? maximum : Math.max(0, Math.min(maximum, currentTop + target.top - viewport.top - 12))
}
