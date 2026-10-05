import assert from 'node:assert/strict'
import test from 'node:test'
import { serializeAttachments, validateAttachments, type MiniFile } from '../src/api/attachment-contract'

const file = (name: string, size = 1024, type = ''): MiniFile => ({ name, size, type, path: `/tmp/${name}` })
test('attachment serializer preserves Go JSON contract for images, Office and UTF-8 text', async () => {
  const reads: string[] = []
  const attachments = await serializeAttachments([file('照片.JPG'), file('预算.xlsx'), file('清单.md', 10, 'text/markdown; charset=utf-8')], {
    read: async (item, encoding) => { reads.push(`${item.name}:${encoding}`); return encoding === 'base64' ? 'AAE=' : '买牛奶🌤️' },
    imageInfo: async () => ({ width: 1024, height: 768 }),
  })
  assert.deepEqual(attachments, [
    { kind: 'image', name: '照片.JPG', mimeType: 'image/jpeg', data: 'AAE=' },
    { kind: 'document', name: '预算.xlsx', mimeType: 'application/octet-stream', data: 'AAE=' },
    { kind: 'file', name: '清单.md', mimeType: 'text/markdown', content: '买牛奶🌤️' },
  ])
  assert.deepEqual(reads.sort(), ['清单.md:utf8', '照片.JPG:base64', '预算.xlsx:base64'].sort())
})
test('attachment size/count/name/format limits reject before reading or submission', () => {
  assert.throws(() => validateAttachments(Array.from({ length: 5 }, () => file('a.txt'))), /最多/)
  assert.throws(() => validateAttachments([file('a.png', 6 * 1024 * 1024 + 1)]), /6 MB/)
  assert.throws(() => validateAttachments([file('a.txt', 4 * 1024 * 1024 + 1)]), /4 MB/)
  assert.throws(() => validateAttachments([file('a.png', 5 * 1024 * 1024), file('b.png', 5 * 1024 * 1024)]), /总大小/)
  assert.throws(() => validateAttachments([file('../secret.txt')]), /名称/)
  assert.throws(() => validateAttachments([file('a.pdf')]), /暂不支持/)
  assert.doesNotThrow(() => validateAttachments([file('Dockerfile'), file('foo.env')]))
})
test('image dimensions and binary masquerading as text fail before dispatch', async () => {
  await assert.rejects(serializeAttachments([file('tiny.png')], { read: async () => 'AA==', imageInfo: async () => ({ width: 10, height: 20 }) }), /宽高/)
  await assert.rejects(serializeAttachments([file('binary.txt')], { read: async () => 'hello\0binary', imageInfo: async () => ({ width: 100, height: 100 }) }), /文本文件/)
})
