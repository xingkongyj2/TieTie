/** Small cancellation primitive for mini-programs without browser globals. */
export interface AbortSignalLike {
  readonly aborted: boolean
  addEventListener(type: 'abort', listener: () => void, options?: { once?: boolean }): void
  removeEventListener(type: 'abort', listener: () => void): void
}
export class AbortSignal implements AbortSignalLike {
  aborted = false
  private listeners = new Map<() => void, boolean>()
  addEventListener(type: 'abort', listener: () => void, options?: { once?: boolean }) {
    if (type === 'abort') this.listeners.set(listener, !!options?.once)
  }
  removeEventListener(type: 'abort', listener: () => void) {
    if (type === 'abort') this.listeners.delete(listener)
  }
  dispatchAbort() {
    if (this.aborted) return
    this.aborted = true
    for (const [listener, once] of [...this.listeners]) {
      if (once) this.listeners.delete(listener)
      listener()
    }
  }
}
export class AbortController {
  readonly signal = new AbortSignal()
  abort() { this.signal.dispatchAbort() }
}
export function abortError(): Error {
  const error = new Error('请求已中止')
  error.name = 'AbortError'
  return error
}
