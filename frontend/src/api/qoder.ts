import { isAccountTokenInvalid, request } from './client'
import type { CloudMember, CloudReminder, Message } from '../types'
import uploadTypes from './upload-types.json'
import { clearToken, getToken } from '../lib/token'

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

export type CloudStreamEvent =
  | { type: 'start'; id: string; kind: 'thinking' | 'message' }
  | { type: 'delta'; id: string; text: string }
  | { type: 'message'; id: string; message: Message | null }
  | { type: 'thinking_end'; id: string }
  | { type: 'status'; id: string; status: string }
  | { type: 'session_error'; id: string; message: string }

interface CloudStream {
  onopen: (() => void) | null
  onerror: (() => void) | null
  onmessage: ((event: { data: string }) => void) | null
  close: () => void
}

/** Fetch SSE carries the account token; native EventSource cannot set this header. */
function authenticatedStream(id: string, after: string | null, privateChannel = false): CloudStream {
  let closed = false
  let cursor = after
  let retry: ReturnType<typeof setTimeout> | null = null
  let controller: AbortController | null = null
  const stream: CloudStream = {
    onopen: null, onerror: null, onmessage: null,
    close() { closed = true; controller?.abort(); if (retry) clearTimeout(retry) },
  }
  const connect = async () => {
    if (closed) return
    controller = new AbortController()
    let reconnect = true
    try {
      const headers = new Headers({ Accept: 'text/event-stream' })
      const token = getToken()
      if (token) headers.set('Authorization', `Bearer ${token}`)
      if (cursor) headers.set('Last-Event-ID', cursor)
      const response = await fetch(`/api/qoder/sessions/${encodeURIComponent(id)}/${privateChannel ? 'private-stream' : 'stream'}`, { headers, signal: controller.signal })
      if (!response.ok) {
        const payload = await response.json().catch(() => null) as { error?: { code?: string } } | null
        if (isAccountTokenInvalid(response.status, payload?.error?.code)) clearToken()
        reconnect = response.status !== 401 && response.status !== 403
        throw new Error(`Stream unavailable: ${response.status}`)
      }
      if (!response.body || !response.headers.get('Content-Type')?.includes('text/event-stream')) throw new Error('Missing event stream')
      stream.onopen?.()
      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      let data: string[] = []
      let eventId: string | null = null
      const consume = (line: string) => {
        if (!line) {
          if (eventId !== null) cursor = eventId || null
          if (data.length) stream.onmessage?.({ data: data.join('\n') })
          data = []; eventId = null
          return
        }
        if (line.startsWith(':')) return
        const colon = line.indexOf(':')
        const field = colon < 0 ? line : line.slice(0, colon)
        const value = colon < 0 ? '' : line.slice(colon + 1).replace(/^ /, '')
        if (field === 'data') data.push(value)
        else if (field === 'id' && !value.includes('\0')) eventId = value
      }
      while (!closed) {
        const { value, done } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        let newline = buffer.indexOf('\n')
        while (newline !== -1) {
          consume(buffer.slice(0, newline).replace(/\r$/, ''))
          buffer = buffer.slice(newline + 1)
          newline = buffer.indexOf('\n')
        }
        if (buffer.length > 2_000_000) throw new Error('Oversized event frame')
      }
    } catch {
      // Polling remains active while SSE reconnects or is unavailable.
    } finally {
      controller?.abort()
      if (!closed) {
        stream.onerror?.()
        if (!closed && reconnect) retry = setTimeout(() => { void connect() }, 3_000)
      }
    }
  }
  void connect()
  return stream
}

const imageTypes = new Set(['image/png', 'image/jpeg', 'image/webp', 'image/gif'])
const imageExtensions: Record<string, string> = { '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.webp': 'image/webp', '.gif': 'image/gif' }
const officeExtensions = /\.(xlsx|xls|xlsm|xlsb|docx|doc)$/i
const textApplicationMimes = new Set(uploadTypes.textApplicationMimes)
const extensionlessNames = new Set(uploadTypes.extensionlessNames)

function supportedTextName(name: string): boolean {
  const lower = name.toLowerCase()
  return extensionlessNames.has(lower) || uploadTypes.textExtensions.some(extension => lower.endsWith(extension))
}

function supportedTextMime(mime: string): boolean {
  const normalized = mime.toLowerCase().split(';')[0].trim()
  return normalized.startsWith('text/') || textApplicationMimes.has(normalized)
}

function imageMimeType(file: File): string | null {
  if (imageTypes.has(file.type)) return file.type
  const extension = file.name.toLowerCase().match(/\.[^.]+$/)?.[0]
  return extension ? imageExtensions[extension] ?? null : null
}

export function isImageAttachment(file: File): boolean { return imageMimeType(file) !== null }
export function isOfficeAttachment(file: File): boolean { return officeExtensions.test(file.name) }

export function validateAttachments(files: File[]): void {
  if (files.length > 4) throw new Error('一次最多添加 4 个附件。')
  let total = 0
  for (const file of files) {
    total += file.size
    if (isImageAttachment(file)) {
      if (file.size > 6 * 1024 * 1024) throw new Error(`${file.name} 超过图片大小限制（6 MB）。`)
    } else if (isOfficeAttachment(file)) {
      if (file.size > 4 * 1024 * 1024) throw new Error(`${file.name} 超过 Office 文件大小限制（4 MB）。`)
    } else if (!(supportedTextName(file.name) || supportedTextMime(file.type))) {
      throw new Error(`此格式暂不支持：${file.name}。可上传文本、Excel、Word 或常见图片。`)
    } else if (file.size > 4 * 1024 * 1024) {
      throw new Error(`${file.name} 超过文本文件大小限制（4 MB）。`)
    }
  }
  if (total > 8 * 1024 * 1024) throw new Error('附件总大小不能超过 8 MB。')
}

async function imageBase64(file: File): Promise<string> {
  let width: number
  let height: number
  if (typeof createImageBitmap === 'function') {
    let bitmap: ImageBitmap
    try { bitmap = await createImageBitmap(file) }
    catch { throw new Error(`无法读取图片：${file.name}`) }
    width = bitmap.width
    height = bitmap.height
    bitmap.close()
  } else {
    const url = URL.createObjectURL(file)
    try {
      const size = await new Promise<{ width: number; height: number }>((resolve, reject) => {
        const image = new Image()
        image.onload = () => resolve({ width: image.naturalWidth, height: image.naturalHeight })
        image.onerror = () => reject(new Error(`无法读取图片：${file.name}`))
        image.src = url
      })
      width = size.width
      height = size.height
    } finally { URL.revokeObjectURL(url) }
  }
  if (width <= 10 || height <= 10 || width > 8000 || height > 8000) {
    throw new Error(`${file.name} 的宽高需各在 11 到 8000 像素之间。`)
  }
  return fileBase64(file)
}

async function fileBase64(file: File): Promise<string> {
  const dataUrl = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(new Error(`读取文件失败：${file.name}`))
    reader.readAsDataURL(file)
  })
  return dataUrl.slice(dataUrl.indexOf(',') + 1)
}

const sessionPath = (id: string) => `/api/qoder/sessions/${encodeURIComponent(id)}/messages`
const remindersPath = (id: string) => `/api/qoder/sessions/${encodeURIComponent(id)}/reminders`

export const qoderApi = {
  getReminders(id: string, signal?: AbortSignal): Promise<{ reminders: CloudReminder[] }> {
    return request(remindersPath(id), { signal })
  },
  createReminder(id: string, input: { title: string; dueAt: string; recipientIds: number[] }): Promise<{ reminder: CloudReminder }> {
    return request(remindersPath(id), { method: 'POST', body: input })
  },
  updateReminder(id: string, reminderId: string, status: 'completed' | 'scheduled' | 'cancelled'): Promise<{ reminder: CloudReminder }> {
    return request(`${remindersPath(id)}/${encodeURIComponent(reminderId)}`, { method: 'PATCH', body: { status } })
  },
  getMessages(id: string, after?: string | null, signal?: AbortSignal): Promise<CloudHistory> {
    const query = after ? `?${new URLSearchParams({ after })}` : ''
    return request(`${sessionPath(id)}${query}`, { signal })
  },
  async sendMessage(id: string, text: string, files: File[] = [], visibility: 'shared' | 'private' = 'shared'): Promise<{ messages: Message[]; replyMode?: 'silent' }> {
    // Never tie a submitted turn to the current view's abort signal.
    validateAttachments(files)
    const attachments = await Promise.all(files.map(async (file) => {
      const imageMime = imageMimeType(file)
      return imageMime
        ? { kind: 'image', name: file.name, mimeType: imageMime, data: await imageBase64(file) }
        : isOfficeAttachment(file)
          ? { kind: 'document', name: file.name, mimeType: file.type || 'application/octet-stream', data: await fileBase64(file) }
        : { kind: 'file', name: file.name, mimeType: supportedTextMime(file.type) ? file.type.toLowerCase().split(';')[0].trim() : 'text/plain', content: await file.text() }
    }))
    return request(sessionPath(id), { method: 'POST', body: { text, attachments, visibility }, timeoutMs: 120_000 })
  },
  /** 回答云端 Agent 抛出的选择题（AskUserQuestion），让挂起的那一轮继续。 */
  sendToolResult(id: string, toolUseId: string, text: string): Promise<{ messages: Message[]; replyMode?: 'silent' }> {
    return request(`/api/qoder/sessions/${encodeURIComponent(id)}/tool-result`, { method: 'POST', body: { toolUseId, text } })
  },
  privateStream(id: string): CloudStream {
    return authenticatedStream(id, null, true)
  },
  stream(id: string, after: string | null): CloudStream {
    return authenticatedStream(id, after)
  },
}
