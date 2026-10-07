export interface WechatLoginProfile {
  nickname: string
  /** Temporary local image returned by the user's chooseAvatar action. */
  avatarPath: string
}

interface WechatLoginDependencies<T> {
  readAvatar: (path: string) => Promise<string>
  login: () => Promise<{ code: string }>
  exchange: (code: string, profile: { nickname: string; avatarBase64: string }) => Promise<T>
}

/** Authenticate first; the server decides whether this identity still needs a profile. */
export async function startWechatLogin<T>({ login, exchange }: {
  login: () => Promise<{ code: string }>
  exchange: (code: string) => Promise<T>
}): Promise<T> {
  let code: string
  try {
    code = (await login()).code?.trim()
  } catch {
    throw new Error('暂时无法连接微信，请稍后重试。')
  }
  if (!code) throw new Error('未获取到微信登录凭证，请重新点击登录。')
  return exchange(code)
}

/** Validate the user's choices before requesting a fresh, single-use login code. */
export async function completeWechatLogin<T>(profile: WechatLoginProfile, { readAvatar, login, exchange }: WechatLoginDependencies<T>): Promise<T> {
  const nickname = profile.nickname.trim()
  if (!nickname || !profile.avatarPath.trim()) throw new Error('请先选择头像并填写昵称，再确认登录。')
  if ([...nickname].length > 24) throw new Error('昵称最多填写 24 个字。')
  const avatarBase64 = await readAvatar(profile.avatarPath)
  if (!avatarBase64) throw new Error('头像读取失败，请重新选择头像。')
  return startWechatLogin({ login, exchange: code => exchange(code, { nickname, avatarBase64 }) })
}
