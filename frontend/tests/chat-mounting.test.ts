import assert from 'node:assert/strict'
import test from 'node:test'
import { HISTORY_BATCH_BYTES, HISTORY_BATCH_COUNT, estimateMountedMessageBytes, matchesMountedPrefix, mountedMessageKey, nextHistoryBatchEnd } from '../src/lib/chatMounting'
import type { Message } from '../src/types'

const message = (index: number): Message => ({
  id: `history-${index}`, sender: index % 2 ? 'ai' : 'self', userId: 7,
  text: '共同安排下周的事情，记得先完成这一项。'.repeat(70),
  createdAt: '2026-10-05T03:15:00Z', time: '11:15',
  images: index % 10 === 0 ? [`wxfile://usr/tietie-image-${index}.png`] : [],
  files: index % 11 === 0 ? ['共同计划.docx'] : [],
  ask: index % 13 === 0 ? [{ question: '哪天比较方便？', multiSelect: true, options: [{ label: '周一' }, { label: '周二' }] }] : [],
})

test('history batches bound UTF-8/native estimates and keep every row, including a large row', () => {
  const rows = Array.from({ length: 90 }, (_, index) => message(index))
  rows[25] = { ...rows[25], text: '大'.repeat(70_000) }
  let start = 0
  let total = 0
  while (start < rows.length) {
    const end = nextHistoryBatchEnd(rows, start)
    assert.ok(end > start && end - start <= HISTORY_BATCH_COUNT)
    const bytes = rows.slice(start, end).reduce((sum, row) => sum + estimateMountedMessageBytes(row), 0)
    assert.ok(bytes <= HISTORY_BATCH_BYTES || end - start === 1)
    total += end - start
    start = end
  }
  assert.equal(total, rows.length)
  assert.ok(matchesMountedPrefix(rows.slice(0, 12).map(mountedMessageKey), [...rows, message(100)]))
  assert.ok(matchesMountedPrefix(rows.slice(0, 12).map(mountedMessageKey), rows.map(row => ({ ...row, text: '更新内容' }))))
  assert.equal(matchesMountedPrefix(rows.slice(0, 12).map(mountedMessageKey), [...rows].reverse()), false)
  assert.equal(matchesMountedPrefix(rows.slice(0, 12).map(mountedMessageKey), rows.slice(1)), false)
})

test('real Taro native hydrate keeps 1,200-row tail batches granular and below setData limits', async (t) => {
  process.env.TARO_ENV = 'weapp'
  process.env.SUPPORT_TARO_POLYFILL = 'disabled'
  // These are webpack constants in the WeChat build, supplied here for Node.
  for (const name of ['INNER_HTML', 'ADJACENT_HTML', 'CLONE_NODE', 'TEMPLATE_CONTENT', 'SIZE_APIS', 'CONTAINS', 'MUTATION_OBSERVER']) {
    (globalThis as Record<string, unknown>)[`ENABLE_${name}`] = false
  }
  const { document, TaroRootElement, hydrate, customWrapperCache } = await import('@tarojs/runtime')
  const calls: Record<string, unknown>[] = []
  const root = new TaroRootElement()
  root.ctx = { setData(data: Record<string, unknown>, callback: () => void) { calls.push(data); callback() } }
  const list = document.createElement('view')
  list.className = 'messages'
  const shell = document.createElement('view')
  const chat = document.createElement('view')
  const scroll = document.createElement('scroll-view')
  const feedbackSlot = document.createElement('view')
  const feedbackText = document.createElement('text')
  feedbackText.appendChild(document.createTextNode('正在回复'))
  feedbackSlot.appendChild(feedbackText)
  scroll.appendChild(list)
  scroll.appendChild(feedbackSlot)
  chat.appendChild(scroll)
  shell.appendChild(chat)
  const mineSlot = document.createElement('view')
  shell.appendChild(mineSlot)
  root.appendChild(shell)
  const flush = () => new Promise<void>(resolve => root.enqueueUpdateCallback(resolve))
  await flush()
  calls.length = 0

  function nativeRow(row: Message) {
    const node = document.createElement('view')
    node.className = 'message-row'
    const avatar = document.createElement('image')
    avatar.setAttribute('src', '/assets/avatars/cream-cat.png')
    avatar.setAttribute('mode', 'aspectFit')
    node.appendChild(avatar)
    const content = document.createElement('view')
    content.className = 'message-content'
    node.appendChild(content)
    const meta = document.createElement('view')
    meta.className = 'message-meta'
    for (const text of ['成员', '11:15']) {
      const label = document.createElement('text')
      label.appendChild(document.createTextNode(text))
      meta.appendChild(label)
    }
    content.appendChild(meta)
    const body = document.createElement('view')
    body.className = 'message-bubble'
    const text = document.createElement('text')
    text.appendChild(document.createTextNode(row.text))
    body.appendChild(text)
    for (const source of row.images ?? []) {
      const image = document.createElement('image')
      image.setAttribute('src', source)
      image.setAttribute('mode', 'aspectFit')
      image.setAttribute('style', 'width:200px;height:150px')
      body.appendChild(image)
    }
    for (const file of row.files ?? []) {
      const fileNode = document.createElement('text')
      fileNode.appendChild(document.createTextNode(file))
      body.appendChild(fileNode)
    }
    for (const question of row.ask ?? []) {
      const ask = document.createElement('view')
      ask.className = 'ask-card'
      const prompt = document.createElement('text')
      prompt.appendChild(document.createTextNode(question.question))
      ask.appendChild(prompt)
      for (const option of question.options ?? []) {
        const button = document.createElement('button')
        button.appendChild(document.createTextNode(option.label))
        ask.appendChild(button)
      }
      body.appendChild(ask)
    }
    content.appendChild(body)
    return node
  }

  const rows = Array.from({ length: 1200 }, (_, index) => message(index))
  let start = 0
  let batchCount = 0
  while (start < rows.length) {
    const end = nextHistoryBatchEnd(rows, start)
    const before = calls.length
    for (const row of rows.slice(start, end)) list.appendChild(nativeRow(row))
    await flush()
    assert.equal(calls.length, before + 1)
    const update = calls.at(-1)!
    assert.ok(Buffer.byteLength(JSON.stringify(update)) < HISTORY_BATCH_BYTES)
    if (start > 0) assert.ok(Object.keys(update).every(path => !path.endsWith('.cn')), 'later commits must update indexed children, not the full cn array')
    batchCount++
    start = end
  }
  assert.equal(list.childNodes.length, rows.length)
  const fullBytes = Buffer.byteLength(JSON.stringify(hydrate(list)))
  const maxBatchBytes = Math.max(...calls.map(data => Buffer.byteLength(JSON.stringify(data))))
  assert.ok(fullBytes > 1024 * 1024, 'the same history would exceed 1 MiB as a single hydrate')

  // Updating a mounted row sends only the changed text path.
  const textNode = list.childNodes[500].childNodes[1].childNodes[1].childNodes[0].childNodes[0]
  textNode.textContent = '已经更新'
  await flush()
  assert.ok(Object.keys(calls.at(-1)!).every(path => path.endsWith('.v')))

  // With stable slots, hiding chat, ending feedback, and opening Mine's sheet
  // never sends the mounted history through an ancestor replacement.
  chat.setAttribute('hidden', true)
  feedbackSlot.removeChild(feedbackText)
  const mine = document.createElement('view')
  mine.className = 'mine-page'
  const sheet = document.createElement('view')
  sheet.className = 'sheet-backdrop'
  const input = document.createElement('input')
  sheet.appendChild(input)
  mine.appendChild(sheet)
  mineSlot.appendChild(mine)
  await flush()
  const ancestorSafeBytes = Buffer.byteLength(JSON.stringify(calls.at(-1)!))
  assert.ok(ancestorSafeBytes < HISTORY_BATCH_BYTES)
  mineSlot.removeChild(mine)
  await flush()
  assert.ok(Buffer.byteLength(JSON.stringify(calls.at(-1))) < 100)

  // The unsafe version (removing the feedback's outer sibling) really does
  // resend all history. This is why the actual native UI keeps that slot.
  scroll.removeChild(feedbackSlot)
  await flush()
  const unsafeAncestorBytes = Buffer.byteLength(JSON.stringify(calls.at(-1)!))
  assert.ok(unsafeAncestorBytes > 1024 * 1024)

  // CustomWrapper redirects descendant paths, but its parent's hydrate still
  // contains wrapper.cn even after the wrapper has its own registered context.
  const wrapper = document.createElement('custom-wrapper')
  customWrapperCache.set(wrapper.sid, root.ctx)
  wrapper.appendChild(list)
  scroll.appendChild(wrapper)
  await flush()
  assert.ok(Buffer.byteLength(JSON.stringify(calls.at(-1))) > 1024 * 1024)
  assert.ok(Buffer.byteLength(JSON.stringify(hydrate(wrapper))) > 1024 * 1024)

  // Clearing before a reordered rebuild transmits an empty array, not history.
  list.textContent = ''
  await flush()
  assert.ok(Buffer.byteLength(JSON.stringify(calls.at(-1))) < 100)
  customWrapperCache.delete(wrapper.sid)
  t.diagnostic(`full hydrate=${fullBytes} bytes; ${batchCount} tail batches; largest setData=${maxBatchBytes} bytes; stable ancestor change=${ancestorSafeBytes} bytes; unsafe ancestor=${unsafeAncestorBytes} bytes`)
})
