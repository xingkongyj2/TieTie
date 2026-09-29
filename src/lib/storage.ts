// The only persistence adapter. Replace these functions with Taro.getStorageSync /
// Taro.setStorageSync during the mini-program migration; domain APIs stay intact.
const memoryStorage = new Map<string, string>()
const fallbackKeys = new Set<string>()

export function readStorage<T>(key: string, fallback: () => T): T {
  let value: string | null | undefined
  try {
    value = typeof window === 'undefined' || fallbackKeys.has(key)
      ? memoryStorage.get(key)
      : window.localStorage.getItem(key) ?? memoryStorage.get(key)
  } catch {
    // Private browsing, disabled storage, and SSR can all use the memory adapter.
    value = memoryStorage.get(key)
  }

  if (!value) return fallback()
  try {
    return JSON.parse(value) as T
  } catch {
    // A partial write / old corrupted JSON must not leave the app on a blank page.
    removeStorage(key)
    return fallback()
  }
}

export function writeStorage<T>(key: string, value: T): void {
  const serialized = JSON.stringify(value)
  memoryStorage.set(key, serialized)
  try {
    if (typeof window !== 'undefined') {
      window.localStorage.setItem(key, serialized)
      fallbackKeys.delete(key)
    }
  } catch {
    // Storage quota / permission errors preserve the current in-memory session.
    // Prefer the newer memory value over any stale previously persisted value.
    fallbackKeys.add(key)
  }
}

export function removeStorage(key: string): void {
  memoryStorage.delete(key)
  fallbackKeys.delete(key)
  try {
    if (typeof window !== 'undefined') window.localStorage.removeItem(key)
  } catch {
    // Removing an unavailable storage entry is a harmless no-op.
  }
}
