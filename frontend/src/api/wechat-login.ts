interface WechatLoginDependencies<T> {
  login: () => Promise<{ code: string }>
  exchange: (code: string) => Promise<T>
}

/** Each attempt obtains a fresh, single-use code; only our backend receives it. */
export async function completeWechatLogin<T>({ login, exchange }: WechatLoginDependencies<T>): Promise<T> {
  let code: string
  try {
    code = (await login()).code?.trim()
  } catch {
    throw new Error('暂时无法连接微信，请稍后重试。')
  }
  if (!code) throw new Error('未获取到微信登录凭证，请重新点击登录。')
  return exchange(code)
}
