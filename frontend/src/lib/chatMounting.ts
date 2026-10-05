import type { Message } from '../types'

export const HISTORY_BATCH_BYTES = 64 * 1024
export const HISTORY_BATCH_COUNT = 12

/** UTF-8 bytes, including JSON escaping, without a browser TextEncoder. */
function serializedBytes(value: unknown): number {
  const text = JSON.stringify(value)
  let bytes = 0
  for (const character of text) {
    const point = character.codePointAt(0)!
    bytes += point <= 0x7f ? 1 : point <= 0x7ff ? 2 : point <= 0xffff ? 3 : 4
  }
  return bytes
}

/** Leave room for native row/date/avatar nodes and Markdown's expanded tree. */
export function estimateMountedMessageBytes(message: Message): number {
  const markdownNodes = message.sender === 'ai' ? (message.text.match(/[\n*_`~#>|[\](){}]/g)?.length ?? 0) : 0
  return 4096 + serializedBytes(message) * 4 + markdownNodes * 96
    + (message.images?.length ?? 0) * 1024 + (message.weatherCards?.length ?? 0) * 4096
}

/** An unusually large single row remains intact and is mounted alone. */
export function nextHistoryBatchEnd(messages: Message[], start: number): number {
  let end = start
  let bytes = 0
  while (end < messages.length && end - start < HISTORY_BATCH_COUNT) {
    const nextBytes = estimateMountedMessageBytes(messages[end])
    if (end > start && bytes + nextBytes > HISTORY_BATCH_BYTES) break
    bytes += nextBytes
    end++
  }
  return end
}

export function mountedMessageKey(message: Message): string {
  const stamp = Date.parse(message.createdAt ?? '')
  const day = Number.isNaN(stamp) ? '' : new Date(stamp + 8 * 60 * 60 * 1000).toISOString().slice(0, 10)
  return `${message.renderKey ?? message.id}:${day}`
}
export function matchesMountedPrefix(keys: string[], messages: Message[]): boolean {
  return keys.length <= messages.length && keys.every((key, index) => key === mountedMessageKey(messages[index]))
}
