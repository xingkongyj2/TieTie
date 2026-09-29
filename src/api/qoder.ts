import { request } from './client'
import type { Message } from '../types'
import uploadTypes from '../../shared/upload-types.json'

export interface CloudSession {
  id: string
  title: string
  status: string
  createdAt: string
  updatedAt: string
  agentName: string
}

export interface CloudSessionList {
  data: CloudSession[]
  defaultSessionId: string | null
}

export interface CloudHistory {
  session: CloudSession
  messages: Message[]
  cursor: string | null
  idleEventId: string | null
  turnError: string | null
}

export type CloudStreamEvent =
  | { type: 'start'; id: string; kind: 'thinking' | 'message' }
  | { type: 'delta'; id: string; text: string }
  | { type: 'message'; id: string; message: Message | null }
  | { type: 'thinking_end'; id: string }
  | { type: 'status'; id: string; status: string }
  | { type: 'session_error'; id: string; message: string }

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

export const qoderApi = {
  listSessions(signal?: AbortSignal): Promise<CloudSessionList> {
    return request('/api/qoder/sessions', { signal })
  },
  getMessages(id: string, after?: string | null, signal?: AbortSignal): Promise<CloudHistory> {
    const query = after ? `?${new URLSearchParams({ after })}` : ''
    return request(`${sessionPath(id)}${query}`, { signal })
  },
  async sendMessage(id: string, text: string, files: File[] = []): Promise<{ messages: Message[] }> {
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
    return request(sessionPath(id), { method: 'POST', body: { text, attachments }, timeoutMs: 120_000 })
  },
  stream(id: string, after: string | null): EventSource {
    const query = after ? `?${new URLSearchParams({ after })}` : ''
    return new EventSource(`/api/qoder/sessions/${encodeURIComponent(id)}/stream${query}`)
  },
}
