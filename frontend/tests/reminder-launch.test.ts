import assert from 'node:assert/strict'
import test from 'node:test'
import { createReminderLaunchStore, reminderSessionFromQuery } from '../src/lib/reminderLaunch'

test('only valid reminder push queries request chat navigation', () => {
  assert.equal(reminderSessionFromQuery({ from: 'reminder', sessionId: 'shared-space' }), 'shared-space')
  for (const query of [{ sessionId: 'space' }, { from: 'reminder' }, { from: 'reminder', sessionId: ' ' },
    { from: 'reminder', sessionId: '\u0000space' }, { from: 'reminder', sessionId: 's'.repeat(161) },
    { from: 'chat', sessionId: 'space' }, { from: 'reminder', sessionId: { value: 'space' } }]) {
    assert.equal(reminderSessionFromQuery(query), null)
  }
})

test('cold push survives before login and subscribers mount, then consumes once', () => {
  const store = createReminderLaunchStore()
  store.receive({ from: 'reminder', sessionId: 'current-space' })
  const pending = store.get()!
  assert.equal(pending.sessionId, 'current-space')
  store.consume(pending.sequence)
  assert.equal(store.get(), null)
})

test('warm push reaches mounted chat and an older consumption never loses a new push', () => {
  const store = createReminderLaunchStore()
  const received: string[] = []
  const dispose = store.subscribe(launch => received.push(launch.sessionId))
  store.receive({ from: 'reminder', sessionId: 'first-space' })
  const first = store.get()!
  store.receive({ from: 'reminder', sessionId: 'second-space' })
  store.consume(first.sequence)
  assert.equal(store.get()?.sessionId, 'second-space')
  assert.deepEqual(received, ['first-space', 'second-space'])
  dispose()
  store.receive({ from: 'reminder', sessionId: 'third-space' })
  assert.equal(received.length, 2)
})
