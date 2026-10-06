export interface ReminderLaunch { sequence: number; sessionId: string }

/** A push link is a navigation hint, never permission to load another session. */
export function reminderSessionFromQuery(query: Record<string, unknown>): string | null {
  if (query.from !== 'reminder' || typeof query.sessionId !== 'string') return null
  const id = query.sessionId.trim()
  return id && id.length <= 160 && !/[\u0000-\u001f]/.test(id) ? id : null
}

export function createReminderLaunchStore() {
  let pending: ReminderLaunch | null = null
  let sequence = 0
  const listeners = new Set<(launch: ReminderLaunch) => void>()
  return {
    get: () => pending,
    receive(query: Record<string, unknown>) {
      const sessionId = reminderSessionFromQuery(query)
      if (!sessionId) return
      pending = { sequence: ++sequence, sessionId }
      for (const listener of listeners) listener(pending)
    },
    consume(expectedSequence: number) {
      if (pending?.sequence === expectedSequence) pending = null
    },
    subscribe(listener: (launch: ReminderLaunch) => void) {
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
  }
}

export const reminderLaunchStore = createReminderLaunchStore()
