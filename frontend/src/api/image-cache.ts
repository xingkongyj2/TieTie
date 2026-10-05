/** Decode inline chat images before they reach WeChat's setData/previewImage. */
export interface InlineImage { data: string; extension: string; bytes: number; digest: string }
export function inlineImage(src: string): InlineImage | null {
  if (!src.startsWith('data:')) return null
  const match = /^data:image\/(png|jpeg|jpg|webp|gif);base64,([A-Za-z0-9+/]*={0,2})$/i.exec(src)
  if (!match || !match[2] || match[2].length % 4 !== 0) throw new Error('聊天图片格式无效。')
  const data = match[2]
  const bytes = data.length / 4 * 3 - (data.endsWith('==') ? 2 : data.endsWith('=') ? 1 : 0)
  if (bytes > 6 * 1024 * 1024) throw new Error('聊天图片超过大小限制。')
  let first = 2166136261; let second = 5381
  for (let index = 0; index < src.length; index++) {
    const code = src.charCodeAt(index)
    first = Math.imul(first ^ code, 16777619)
    second = Math.imul(second, 33) ^ code
  }
  const digest = `${(first >>> 0).toString(16)}-${(second >>> 0).toString(16)}-${bytes}`
  return { data, extension: /jpe?g/i.test(match[1]) ? 'jpg' : match[1].toLowerCase(), bytes, digest }
}
export interface ImageFileStore {
  directory: string
  write(path: string, base64: string): Promise<void>
  remove(path: string): Promise<void>
  list(): Promise<string[]>
}
export function createImageCache(store: ImageFileStore, instance = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`) {
  let generation = 0
  const cache = new Map<string, Promise<string>>()
  const prefix = () => `tietie-image-${instance}-${generation}-`
  return {
    resolve(src: string): Promise<string> {
      const image = inlineImage(src)
      if (!image) return Promise.resolve(src)
      const key = `${image.digest}.${image.extension}`
      const cached = cache.get(key)
      if (cached) return cached
      const version = generation
      const path = `${store.directory}/${prefix()}${image.digest}.${image.extension}`
      const pending = store.write(path, image.data).then(async () => {
        if (version !== generation) {
          await store.remove(path).catch(() => {})
          throw new Error('图片所属的账号会话已结束。')
        }
        return path
      }).catch(error => {
        if (cache.get(key) === pending) cache.delete(key)
        throw error
      })
      cache.set(key, pending)
      return pending
    },
    async clear(): Promise<void> {
      generation++
      cache.clear()
      // Include cache files left by an earlier app launch, but spare new writes.
      const names = await store.list().catch(() => [])
      const currentPrefix = prefix()
      await Promise.all(names.filter(name => name.startsWith('tietie-image-') && !name.startsWith(currentPrefix))
        .map(name => store.remove(`${store.directory}/${name}`).catch(() => {})))
    },
  }
}
