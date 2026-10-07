import Taro from '@tarojs/taro'

const MAX_AVATAR_BYTES = 2 * 1024 * 1024

/** Read only the local file selected by chooseAvatar; the server validates image contents. */
export async function readWechatAvatar(path: string): Promise<string> {
  if (process.env.TARO_ENV !== 'weapp') throw new Error('请在微信小程序中选择头像。')
  if (!path.trim()) throw new Error('请先选择头像。')
  const fs = Taro.getFileSystemManager()
  const size = await new Promise<number>((resolve, reject) => {
    fs.getFileInfo({ filePath: path,
      success: result => resolve(result.size),
      fail: () => reject(new Error('头像已失效，请重新选择头像。')),
    })
  })
  if (!size || size > MAX_AVATAR_BYTES) throw new Error('请选择不超过 2 MB 的头像。')
  return new Promise<string>((resolve, reject) => {
    fs.readFile({ filePath: path, encoding: 'base64',
      success: result => {
        if (typeof result.data !== 'string' || !result.data) reject(new Error('头像读取失败，请重新选择头像。'))
        else if (result.data.length > Math.ceil(MAX_AVATAR_BYTES / 3) * 4) reject(new Error('请选择不超过 2 MB 的头像。'))
        else resolve(result.data)
      },
      fail: () => reject(new Error('头像读取失败，请重新选择头像。')),
    })
  })
}
