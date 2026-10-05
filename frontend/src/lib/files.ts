import Taro from '@tarojs/taro'
import { imageMimeType, serializeAttachments, validateAttachments, type MiniFile } from '../api/attachment-contract'
import { createImageCache } from '../api/image-cache'

export type { MiniFile } from '../api/attachment-contract'
export { isImageAttachment, isOfficeAttachment, validateAttachments } from '../api/attachment-contract'

function fileName(path: string, index: number) { return path.split('/').pop()?.split('?')[0] || `附件-${index + 1}` }
function inferMime(name: string): string {
  const extension = name.toLowerCase().match(/\.[^.]+$/)?.[0] ?? ''
  const types: Record<string, string> = { '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.gif': 'image/gif', '.webp': 'image/webp', '.docx': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', '.xlsx': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', '.xlsm': 'application/vnd.ms-excel.sheet.macroEnabled.12' }
  return types[extension] ?? ''
}
export async function chooseAttachments(remainingCount = 4): Promise<MiniFile[]> {
  const count = Math.max(0, Math.min(4, Math.floor(remainingCount)))
  if (!count) throw new Error('一次最多添加 4 个附件。')
  try {
    const { tapIndex } = await Taro.showActionSheet({ itemList: ['图片 / 拍照', '聊天中的文件'] })
    let files: MiniFile[]
    if (tapIndex === 0) {
      const result = await Taro.chooseMedia({ count, mediaType: ['image'], sourceType: ['album', 'camera'], sizeType: ['original'] })
      files = await Promise.all(result.tempFiles.map(async (item, index) => {
        const path = item.tempFilePath
        let name = fileName(path, index)
        let type = inferMime(name)
        if (!imageMimeType({ name, type, path, size: item.size })) {
          const info = await Taro.getImageInfo({ src: path })
          const format = info.type === 'jpg' ? 'jpeg' : info.type
          type = `image/${format}`
          name = `图片-${Date.now()}-${index + 1}.${format === 'jpeg' ? 'jpg' : format}`
        }
        return { path, name, type, size: item.size }
      }))
    } else {
      const result = await Taro.chooseMessageFile({ count, type: 'file' })
      files = result.tempFiles.map(item => ({ name: item.name, size: item.size, type: inferMime(item.name), path: item.path }))
    }
    validateAttachments(files)
    return files
  } catch (error) {
    if (/cancel/i.test((error as { errMsg?: string })?.errMsg ?? '')) return []
    if (error instanceof Error) throw error
    throw new Error('没有读取到附件，请重新选择。')
  }
}
export function readMiniFile(file: MiniFile, encoding: 'base64' | 'utf8'): Promise<string> {
  return new Promise((resolve, reject) => {
    Taro.getFileSystemManager().readFile({ filePath: file.path, encoding,
      success: result => { if (typeof result.data === 'string') resolve(result.data); else reject(new Error(`读取文件失败：${file.name}`)) },
      fail: () => reject(new Error(`读取文件失败：${file.name}`)),
    })
  })
}
export const prepareAttachments = (files: MiniFile[]) => serializeAttachments(files, {
  read: readMiniFile,
  imageInfo: async file => {
    try { return await Taro.getImageInfo({ src: file.path }) }
    catch { throw new Error(`无法读取图片：${file.name}`) }
  },
})

let previewCache: ReturnType<typeof createImageCache> | undefined
function getPreviewCache() {
  if (previewCache) return previewCache
  const directory = Taro.env.USER_DATA_PATH
  if (!directory) throw new Error('无法访问小程序图片缓存目录。')
  const fs = Taro.getFileSystemManager()
  previewCache = createImageCache({ directory,
    write: (path, data) => new Promise((resolve, reject) => fs.writeFile({ filePath: path, data, encoding: 'base64', success: () => resolve(), fail: () => reject(new Error('聊天图片保存失败，请释放空间后重试。')) })),
    remove: path => new Promise((resolve, reject) => fs.unlink({ filePath: path, success: () => resolve(), fail: reject })),
    list: () => new Promise((resolve, reject) => fs.readdir({ dirPath: directory, success: result => resolve(result.files), fail: reject })),
  })
  return previewCache
}
export function imagePreviewPath(src: string): Promise<string> {
  if (process.env.TARO_ENV !== 'weapp' || !src.startsWith('data:')) return Promise.resolve(src)
  try { return getPreviewCache().resolve(src) }
  catch (error) { return Promise.reject(error) }
}
export async function clearImagePreviewCache(): Promise<void> {
  if (process.env.TARO_ENV !== 'weapp') return
  try { await getPreviewCache().clear() }
  catch { /* Cache cleanup must never block account changes. */ }
}
