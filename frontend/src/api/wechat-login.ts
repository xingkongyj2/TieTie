interface WechatLoginDependencies<T> {
  login: () => Promise<{ code: string }>
  /** Optional display profile prompt; declining it must not block login. */
  getProfile?: () => Promise<{ nickname: string; avatarUrl: string }>
  exchange: (code: string, profile?: { nickname: string; avatarUrl: string }) => Promise<T>
}

/** Each attempt obtains a fresh, single-use code; only our backend receives it. */
export async function completeWechatLogin<T>({ login, getProfile, exchange }: WechatLoginDependencies<T>): Promise<T> {
  let profile: { nickname: string; avatarUrl: string } | undefined
  if (getProfile) {
    try {
      const value = await getProfile()
      const nickname = String(value?.nickname ?? '').trim()
      const avatarUrl = String(value?.avatarUrl ?? '').trim()
      if (nickname || avatarUrl) profile = { nickname, avatarUrl }
    } catch {
      // The user may decline the optional profile prompt. Login still proceeds
      // and the server selects a local fallback profile/avatar.
    }
  }
  let code: string
  try {
    code = (await login()).code?.trim()
  } catch {
    throw new Error('暂时无法连接微信，请稍后重试。')
  }
  if (!code) throw new Error('未获取到微信登录凭证，请重新点击登录。')
  return exchange(code, profile)
}
