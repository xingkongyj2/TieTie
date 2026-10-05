// JWT 令牌存取：登录成功后保存，请求层自动附带，401 时清除。
import { readStorage, removeStorage, writeStorage } from './storage'

const TOKEN_KEY = 'tietie.token.v1'

export function getToken(): string | null {
  return readStorage<string | null>(TOKEN_KEY, () => null)
}

export function setToken(token: string): void {
  writeStorage(TOKEN_KEY, token)
}

export function clearToken(): void {
  removeStorage(TOKEN_KEY)
}

const invalidationListeners = new Set<() => void>()
export function onAccountTokenInvalid(listener: () => void): () => void {
  invalidationListeners.add(listener)
  return () => { invalidationListeners.delete(listener) }
}
export function invalidateToken(expectedToken: string): void {
  if (getToken() !== expectedToken) return
  clearToken()
  for (const listener of [...invalidationListeners]) listener()
}
