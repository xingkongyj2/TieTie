/** Streaming UTF-8 decoder; mini-program logic runtimes need not expose TextDecoder. */
export class Utf8Decoder {
  private pending = new Uint8Array(0)
  decode(chunk: ArrayBuffer | Uint8Array): string {
    const incoming = chunk instanceof Uint8Array ? chunk : new Uint8Array(chunk)
    const bytes = new Uint8Array(this.pending.length + incoming.length)
    bytes.set(this.pending); bytes.set(incoming, this.pending.length)
    this.pending = new Uint8Array(0)
    let output = ''
    for (let at = 0; at < bytes.length;) {
      const first = bytes[at]
      if (first < 0x80) { output += String.fromCharCode(first); at++; continue }
      const length = first >= 0xc2 && first <= 0xdf ? 2 : first >= 0xe0 && first <= 0xef ? 3 : first >= 0xf0 && first <= 0xf4 ? 4 : 0
      if (!length) { output += '\ufffd'; at++; continue }
      if (at + length > bytes.length) { this.pending = bytes.slice(at); break }
      let valid = true
      for (let offset = 1; offset < length; offset++) if ((bytes[at + offset] & 0xc0) !== 0x80) valid = false
      const second = bytes[at + 1]
      if (first === 0xe0 && second < 0xa0 || first === 0xed && second > 0x9f || first === 0xf0 && second < 0x90 || first === 0xf4 && second > 0x8f) valid = false
      if (!valid) { output += '\ufffd'; at++; continue }
      let code = first & (length === 2 ? 0x1f : length === 3 ? 0x0f : 0x07)
      for (let offset = 1; offset < length; offset++) code = (code << 6) | (bytes[at + offset] & 0x3f)
      output += String.fromCodePoint(code)
      at += length
    }
    return output
  }
}

export interface SSEEvent { data: string; id: string | null }
/** Cursor commits only at a complete frame, so disconnects never skip partial events. */
export class SSEParser {
  private decoder = new Utf8Decoder()
  private buffer = ''
  private data: string[] = []
  private eventId: string | null = null
  private frameSize = 0
  private first = true
  cursor: string | null
  constructor(private onEvent: (event: SSEEvent) => void, cursor: string | null = null, private maxFrameSize = 2_000_000) {
    this.cursor = cursor
  }
  push(chunk: ArrayBuffer | Uint8Array) {
    let decoded = this.decoder.decode(chunk)
    if (this.first && decoded) { this.first = false; decoded = decoded.replace(/^\ufeff/, '') }
    this.buffer += decoded
    while (true) {
      const at = this.buffer.search(/[\r\n]/)
      if (at < 0 || this.buffer[at] === '\r' && at === this.buffer.length - 1) break
      const line = this.buffer.slice(0, at)
      const width = this.buffer[at] === '\r' && this.buffer[at + 1] === '\n' ? 2 : 1
      this.buffer = this.buffer.slice(at + width)
      this.frameSize += line.length + width
      if (this.frameSize > this.maxFrameSize) throw new Error('事件内容过大。')
      if (line === '') {
        if (this.eventId !== null) this.cursor = this.eventId || null
        const data = this.data
        this.data = []; this.eventId = null; this.frameSize = 0
        if (data.length) this.onEvent({ data: data.join('\n'), id: this.cursor })
      } else if (!line.startsWith(':')) {
        const colon = line.indexOf(':')
        const field = colon < 0 ? line : line.slice(0, colon)
        const value = colon < 0 ? '' : line.slice(colon + 1).replace(/^ /, '')
        if (field === 'data') this.data.push(value)
        else if (field === 'id' && !value.includes('\0')) this.eventId = value
      }
    }
    if (this.frameSize + this.buffer.length > this.maxFrameSize) throw new Error('事件内容过大。')
  }
}
