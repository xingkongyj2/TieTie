import Taro from '@tarojs/taro'
import { ApiError, request } from './client'
import { apiURL } from '../config/api'
import { createAuthenticatedStream } from './stream'
import { consumeTaskRejection } from './task'
import { isAppVisible, onAppVisibilityChange } from '../lib/platform'
import { invalidateToken, getToken } from '../lib/token'
import type { AbortSignalLike } from '../lib/abort'
import { imagePreviewPath, prepareAttachments, type MiniFile } from '../lib/files'
export { isExcelAttachment, isImageAttachment, isOfficeAttachment, validateAttachments } from './attachment-contract'
import type { CloudMember, CloudReminder, Message, ReminderRecurrence } from '../types'

export interface CloudSession {
  replyMode?: 'silent'
  id: string
  title: string
  status: string
  createdAt: string
  updatedAt: string
  agentName: string
}

export interface CloudHistory {
  session: CloudSession
  messages: Message[]
  cursor: string | null
  idleEventId: string | null
  turnError: string | null
  members?: CloudMember[]
  reminders?: CloudReminder[]
  remindersError?: string | null
}

export class SendCancelledError extends Error {
  constructor() { super('发送已停止'); this.name = 'SendCancelledError' }
}

export type CloudStreamEvent =
  | { type: 'start'; id: string; kind: 'thinking' | 'message'; proactive?: 'reminder' | 'update' }
  | { type: 'delta'; id: string; text: string }
  | { type: 'message'; id: string; message: Message | null }
  | { type: 'thinking_end'; id: string }
  | { type: 'status'; id: string; status: string }
  | { type: 'session_error'; id: string; message: string }

async function localImages(message: Message): Promise<Message> {
  if (!message.images?.length) return message
  return { ...message, images: await Promise.all(message.images.map(imagePreviewPath)) }
}
async function localMessages<T extends { messages: Message[] }>(result: T): Promise<T> {
  return { ...result, messages: await Promise.all(result.messages.map(localImages)) }
}
async function submittedMessages<T extends { messages: Message[] }>(result: T): Promise<T> {
  try { return await localMessages(result) }
  catch {
    // The POST already succeeded; never restore its draft as a failed send.
    throw new ApiError('消息已提交，但图片暂时无法加载，请同步记录后查看，勿重复发送。', 200, 'LOCAL_IMAGE_ERROR')
  }
}

function authenticatedStream(id: string, after: string | null, privateChannel = false) {
  return createAuthenticatedStream({
    url: apiURL, getToken, invalidateToken,
    request: options => consumeTaskRejection(Taro.request(options)),
    isVisible: isAppVisible, onVisibilityChange: onAppVisibilityChange,
    maxFrameSize: 12 * 1024 * 1024,
    transformData: async data => {
      const event = JSON.parse(data) as CloudStreamEvent
      if (event.type !== 'message' || !event.message?.images?.length) return data
      return JSON.stringify({ ...event, message: await localImages(event.message) })
    },
  }, `/api/qoder/sessions/${encodeURIComponent(id)}/${privateChannel ? 'private-stream' : 'stream'}`, after)
}

const sessionPath = (id: string) => `/api/qoder/sessions/${encodeURIComponent(id)}/messages`
const remindersPath = (id: string) => `/api/qoder/sessions/${encodeURIComponent(id)}/reminders`

export const qoderApi = {
  async getCachedMessages(id: string, signal?: AbortSignalLike): Promise<{ messages: Message[] }> {
    return localMessages(await request<{ messages: Message[] }>(`/api/qoder/sessions/${encodeURIComponent(id)}/message-preview`, { signal }))
  },
  getReminders(id: string, signal?: AbortSignalLike): Promise<{ reminders: CloudReminder[] }> {
    return request(remindersPath(id), { signal })
  },
  createReminder(id: string, input: { title: string; dueAt: string; recipientIds: number[]; recurrence?: ReminderRecurrence }): Promise<{ reminder: CloudReminder }> {
    return request(remindersPath(id), { method: 'POST', body: input })
  },
  updateReminder(id: string, reminderId: string, status: 'completed' | 'scheduled' | 'cancelled'): Promise<{ reminder: CloudReminder }> {
    return request(`${remindersPath(id)}/${encodeURIComponent(reminderId)}`, { method: 'PATCH', body: { status } })
  },
  deleteReminder(id: string, reminderId: string): Promise<void> {
    return request(`${remindersPath(id)}/${encodeURIComponent(reminderId)}`, { method: 'DELETE' })
  },
  async getMessages(id: string, after?: string | null, signal?: AbortSignalLike, careAfter?: string, timeoutMs = 120_000): Promise<CloudHistory> {
    const query = after ? `?after=${encodeURIComponent(after)}` : ''
    return localMessages(await request<CloudHistory>(`${sessionPath(id)}${query}`, { signal, timeoutMs, headers: careAfter ? { 'X-Tietie-Care-After': careAfter } : undefined }))
  },
  async sendMessage(id: string, text: string, files: MiniFile[] = [], visibility: 'shared' | 'private' = 'shared', beforeDispatch?: () => boolean): Promise<{ messages: Message[]; replyMode?: 'silent' }> {
    // Never tie a submitted turn to the current view's abort signal.
    const attachments = await prepareAttachments(files)
    if (beforeDispatch && !beforeDispatch()) throw new SendCancelledError()
    return submittedMessages(await request<{ messages: Message[]; replyMode?: 'silent' }>(sessionPath(id), { method: 'POST', body: { text, attachments, visibility }, timeoutMs: 120_000 }))
  },
  async cancelTurn(id: string, visibility: 'shared' | 'private'): Promise<{ status: string }> {
    try {
      return await request(`/api/qoder/sessions/${encodeURIComponent(id)}/cancel`, { method: 'POST', body: { visibility }, timeoutMs: 150_000 })
    } catch (error) {
      if (error instanceof ApiError && (error.status === 0 || error.status >= 500)) {
        throw new Error('停止请求暂未确认，请查看处理状态后重试。')
      }
      throw error
    }
  },
  /** 回答云端 Agent 抛出的选择题（AskUserQuestion），让挂起的那一轮继续。 */
  async sendToolResult(id: string, toolUseId: string, text: string): Promise<{ messages: Message[]; replyMode?: 'silent' }> {
    return submittedMessages(await request<{ messages: Message[]; replyMode?: 'silent' }>(`/api/qoder/sessions/${encodeURIComponent(id)}/tool-result`, { method: 'POST', body: { toolUseId, text } }))
  },
  privateStream(id: string) {
    return authenticatedStream(id, null, true)
  },
  stream(id: string, after: string | null) {
    return authenticatedStream(id, after)
  },
}
