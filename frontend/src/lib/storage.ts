import Taro from '@tarojs/taro'

const memoryStorage = new Map<string, string>()
const fallbackKeys = new Set<string>()
export function readStorage<T>(key: string, fallback: () => T): T {
  let value: unknown
  try { value = fallbackKeys.has(key) ? memoryStorage.get(key) : Taro.getStorageSync(key) || memoryStorage.get(key) }
  catch { value = memoryStorage.get(key) }
  if (!value) return fallback()
  try { return (typeof value === 'string' ? JSON.parse(value) : value) as T }
  catch { removeStorage(key); return fallback() }
}
export function writeStorage<T>(key: string, value: T): void {
  const serialized = JSON.stringify(value)
  memoryStorage.set(key, serialized)
  try { Taro.setStorageSync(key, serialized); fallbackKeys.delete(key) }
  catch { fallbackKeys.add(key) }
}
export function removeStorage(key: string): void {
  memoryStorage.delete(key)
  // Failed removals must not resurrect an older persisted JWT on the next read.
  fallbackKeys.add(key)
  try { Taro.removeStorageSync(key) } catch { /* Tombstone stays authoritative. */ }
}
