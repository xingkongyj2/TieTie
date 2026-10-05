import assert from 'node:assert/strict'
import test from 'node:test'
import { createImageCache, inlineImage } from '../src/api/image-cache'

const image = 'data:image/png;base64,aGVsbG8='
test('inline images use safe content-derived file names and reject unsupported/oversized data', () => {
  const parsed = inlineImage(image)!
  assert.equal(parsed.extension, 'png'); assert.equal(parsed.bytes, 5)
  assert.match(parsed.digest, /^[a-f0-9]+-[a-f0-9]+-5$/)
  assert.equal(inlineImage('https://images.example/photo.png'), null)
  assert.equal(inlineImage('data:image/jpeg;base64,aGVsbG8=')?.extension, 'jpg')
  assert.throws(() => inlineImage('data:image/svg+xml;base64,aGVsbG8='), /格式/)
  assert.throws(() => inlineImage('data:image/png;base64,YQ='), /格式/)
  assert.throws(() => inlineImage(`data:image/png;base64,${'AAAA'.repeat(2 * 1024 * 1024 + 1)}`), /大小/)
})
test('image cache deduplicates concurrent history/stream reads and clears only its own files', async () => {
  const files = new Map<string, string>([['/data/tietie-image-old-0-photo.png', 'old'], ['/data/settings.json', '{}']])
  let writes = 0
  const cache = createImageCache({ directory: '/data',
    write: async (path, data) => { writes++; files.set(path, data) },
    remove: async path => { files.delete(path) },
    list: async () => [...files.keys()].map(path => path.slice('/data/'.length)),
  }, 'test')
  const [first, second] = await Promise.all([cache.resolve(image), cache.resolve(image)])
  assert.equal(first, second); assert.equal(writes, 1)
  assert.equal(files.get(first), 'aGVsbG8=')
  await cache.clear()
  assert.deepEqual([...files.keys()], ['/data/settings.json'])
  const next = await cache.resolve(image)
  assert.notEqual(next, first); assert.equal(writes, 2)
})
test('account changes reject and remove image writes still in flight', async () => {
  const files = new Set<string>()
  let complete: (() => void) | undefined
  const cache = createImageCache({ directory: '/data',
    write: (path) => new Promise(resolve => { complete = () => { files.add(path); resolve() } }),
    remove: async path => { files.delete(path) },
    list: async () => [],
  }, 'test')
  const pending = cache.resolve(image)
  await cache.clear()
  complete!()
  await assert.rejects(pending, /会话已结束/)
  assert.equal(files.size, 0)
})
test('failed image writes may be retried without caching a rejected result', async () => {
  let writes = 0
  const cache = createImageCache({ directory: '/data', write: async () => { if (++writes === 1) throw new Error('full') }, remove: async () => {}, list: async () => [] }, 'test')
  await assert.rejects(cache.resolve(image), /full/)
  assert.match(await cache.resolve(image), /^\/data\/tietie-image-test-/)
  assert.equal(writes, 2)
})
