import uploadTypes from './upload-types.json'

export interface MiniFile { name: string; size: number; type: string; path: string }
const imageTypes = new Set(['image/png', 'image/jpeg', 'image/webp', 'image/gif'])
const imageExtensions: Record<string, string> = { '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.webp': 'image/webp', '.gif': 'image/gif' }
const textApplicationMimes = new Set(uploadTypes.textApplicationMimes)
const extensionlessNames = new Set(uploadTypes.extensionlessNames)
export function imageMimeType(file: MiniFile): string | null {
  if (imageTypes.has(file.type)) return file.type
  const extension = file.name.toLowerCase().match(/\.[^.]+$/)?.[0]
  return extension ? imageExtensions[extension] ?? null : null
}
export function isImageAttachment(file: MiniFile) { return imageMimeType(file) !== null }
export function isOfficeAttachment(file: MiniFile) { return /\.(xlsx|xls|xlsm|xlsb|docx|doc)$/i.test(file.name) }
export function supportedTextName(name: string): boolean {
  const lower = name.toLowerCase()
  return extensionlessNames.has(lower) || uploadTypes.textExtensions.some(extension => lower.endsWith(extension))
}
export function supportedTextMime(mime: string): boolean {
  const normalized = mime.toLowerCase().split(';')[0].trim()
  return normalized.startsWith('text/') || textApplicationMimes.has(normalized)
}
export function validateAttachments(files: MiniFile[]): void {
  if (files.length > 4) throw new Error('一次最多添加 4 个附件。')
  let total = 0
  for (const file of files) {
    if (!file.name || Array.from(file.name).length > 255 || /[\\/\x00-\x1f\x7f]/.test(file.name)) throw new Error('附件名称无效。')
    if (!Number.isFinite(file.size) || file.size <= 0 || !file.path) throw new Error(`${file.name} 为空或已无法读取。`)
    total += file.size
    if (isImageAttachment(file)) {
      if (file.size > 6 * 1024 * 1024) throw new Error(`${file.name} 超过图片大小限制（6 MB）。`)
    } else if (isOfficeAttachment(file)) {
      if (file.size > 4 * 1024 * 1024) throw new Error(`${file.name} 超过 Office 文件大小限制（4 MB）。`)
    } else if (!(supportedTextName(file.name) || supportedTextMime(file.type))) {
      throw new Error(`此格式暂不支持：${file.name}。可上传文本、Excel、Word 或常见图片。`)
    } else if (file.size > 4 * 1024 * 1024) throw new Error(`${file.name} 超过文本文件大小限制（4 MB）。`)
  }
  if (total > 8 * 1024 * 1024) throw new Error('附件总大小不能超过 8 MB。')
}
export type Attachment = { kind: 'image' | 'document'; name: string; mimeType: string; data: string } | { kind: 'file'; name: string; mimeType: string; content: string }
export interface AttachmentReader {
  read(file: MiniFile, encoding: 'base64' | 'utf8'): Promise<string>
  imageInfo(file: MiniFile): Promise<{ width: number; height: number }>
}
export async function serializeAttachments(files: MiniFile[], reader: AttachmentReader): Promise<Attachment[]> {
  validateAttachments(files)
  return Promise.all(files.map(async file => {
    const mime = imageMimeType(file)
    if (mime) {
      const { width, height } = await reader.imageInfo(file)
      if (width <= 10 || height <= 10 || width > 8000 || height > 8000) throw new Error(`${file.name} 的宽高需各在 11 到 8000 像素之间。`)
      return { kind: 'image', name: file.name, mimeType: mime, data: await reader.read(file, 'base64') }
    }
    if (isOfficeAttachment(file)) return { kind: 'document', name: file.name, mimeType: file.type || 'application/octet-stream', data: await reader.read(file, 'base64') }
    const content = await reader.read(file, 'utf8')
    if (!content || content.includes('\0')) throw new Error(`${file.name} 不是可读取的文本文件。`)
    return { kind: 'file', name: file.name, mimeType: supportedTextMime(file.type) ? file.type.toLowerCase().split(';')[0].trim() : 'text/plain', content }
  }))
}
