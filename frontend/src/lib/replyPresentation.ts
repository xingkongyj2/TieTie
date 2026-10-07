import type { Message } from '../types'

export type ReplyPhase = 'sending' | 'waiting' | 'thinking' | 'replying' | 'syncing' | 'delayed' | 'stopping' | 'stopped' | 'sent' | 'error' | 'complete' | 'proactive_reminder' | 'proactive_update'
export interface ReplyFeedback { phase: ReplyPhase; message?: string }

interface Snapshot {
  owner: string
  messages: Message[]
  loaded: boolean
  busy: boolean
  terminated?: boolean
  silent: boolean
  turnError: string
  feedback: ReplyFeedback | null
  pending?: { startedAt: number; silent: boolean; visibility: 'shared' | 'private' }
}

interface ReplySlot {
  message: Message
  previousIds: Set<string>
  source: Message['source']
  after: number
  replying: boolean
}

/** UI rows are kept separate from authoritative history, cursors and API writes. */
export class ReplyPresentation {
  private owner = ''
  private sequence = 0
  private lastTurn = ''
  private slot: ReplySlot | null = null
  private keys = new Map<string, { key: string; createdAt?: string }>()
  private rows: Message[] = []

  present(snapshot: Snapshot): Message[] {
    if (snapshot.owner !== this.owner) {
      this.owner = snapshot.owner
      this.lastTurn = ''
      this.slot = null
      this.keys.clear()
      this.rows = []
    }
    const { messages, feedback, pending } = snapshot
    const tail = messages.at(-1)
    const proactiveSource = feedback?.phase === 'proactive_reminder' ? 'reminder'
      : feedback?.phase === 'proactive_update' ? 'reminder_update' : undefined
    const token = pending && !pending.silent ? `send:${pending.startedAt}`
      : snapshot.loaded && !snapshot.silent && !snapshot.turnError
        && tail?.localStatus !== 'failed'
        && (proactiveSource || snapshot.busy && tail?.sender !== 'ai')
        ? `${proactiveSource ?? 'chat'}:${tail?.id ?? 'welcome'}` : ''
    if (token && token !== this.lastTurn && (!this.slot || pending && !pending.silent
      || ['error', 'stopped'].includes(this.slot.message.replyStatus!.phase)
        && tail && !this.slot.previousIds.has(tail.id))) {
      this.lastTurn = token
      const now = new Date(pending?.startedAt ?? Date.now())
      const id = `reply_${++this.sequence}`
      this.slot = {
        message: { id, renderKey: id, sender: 'ai', source: proactiveSource ?? 'chat', kind: 'text',
          text: '', time: '', createdAt: now.toISOString(), visibility: pending?.visibility ?? 'shared', streaming: true,
          replyStatus: { phase: 'thinking', welcome: !messages.length } },
        previousIds: new Set(messages.map(message => message.id)),
        source: proactiveSource ?? 'chat',
        after: Date.parse(tail?.createdAt ?? '') || 0,
        replying: false,
      }
    }

    const slot = this.slot
    if (slot) {
      // A reminder or a backfilled old reply must not consume a pending chat row.
      const reply = messages.find(message => message.sender === 'ai' && !message.streaming
        && (message.source ?? 'chat') === slot.source && !slot.previousIds.has(message.id)
        && (message.visibility ?? 'shared') === slot.message.visibility
        && (!message.createdAt || Date.parse(message.createdAt) >= slot.after))
      if (reply) {
        // Keep the original row's day separator even if generation crosses
        // midnight. The authoritative event timestamp is left untouched.
        this.keys.set(reply.id, { key: slot.message.renderKey!, createdAt: slot.message.createdAt })
        this.slot = null
      } else if (pending?.silent || feedback?.phase === 'sent'
        || !pending && !feedback && tail?.localStatus === 'failed') {
        this.slot = null
      } else {
        const previous = slot.message.replyStatus!
        if (feedback?.phase === 'replying') slot.replying = true
        // Tool turns may emit idle/thinking repeatedly. Only a real reply start
        // advances the visible stage; syncing and polling never rewind it.
        const phase = feedback?.phase === 'error' || snapshot.turnError || snapshot.terminated || previous.phase === 'error' ? 'error'
          : feedback?.phase === 'stopped' || previous.phase === 'stopped' ? 'stopped'
            : feedback?.phase === 'stopping' ? 'stopping'
              : slot.replying ? 'replying' : 'thinking'
        const message = phase === 'error' ? feedback?.message || snapshot.turnError || previous.message || '这次回复遇到问题，请重试。' : undefined
        if (phase !== previous.phase || message !== previous.message) {
          slot.message = { ...slot.message, replyStatus: { ...previous, phase, message } }
        }
      }
    }

    const previousRows = new Map(this.rows.map(row => [row.id, row]))
    const rows = messages.map(message => {
      const identity = this.keys.get(message.id)
      if (!identity) return message
      if (identity.key === message.renderKey && identity.createdAt === message.createdAt) return message
      const previous = previousRows.get(message.id)
      // Preserve object identity on status-only updates as well as React keys.
      const fields = new Set([...Object.keys(message), ...Object.keys(previous ?? {})])
      return previous && [...fields].every(field => field === 'createdAt' || field === 'renderKey'
        || previous[field as keyof Message] === message[field as keyof Message])
        ? previous : { ...message, renderKey: identity.key, createdAt: identity.createdAt }
    })
    if (this.slot) rows.push(this.slot.message)
    if (rows.length !== this.rows.length || rows.some((row, index) => row !== this.rows[index])) this.rows = rows
    return this.rows
  }
}

export function replyStatusText(status: NonNullable<Message['replyStatus']>, name: string): string {
  return status.phase === 'error' ? status.message || '这次回复遇到问题，请重试。'
    : status.phase === 'stopped' ? '已停止回复'
      : status.phase === 'stopping' ? '正在停止回复…'
        : status.phase === 'replying' ? `${name}正在回复中`
          : status.welcome ? `${name}正在为你们准备欢迎语` : `${name}正在思考中`
}
