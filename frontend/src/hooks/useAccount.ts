import { useCallback, useEffect, useState } from 'react'
import { authApi, type AccountResult } from '../api/auth'
import { clearToken, getToken } from '../lib/token'

/**
 * 账号状态：启动时用本地 JWT 拉取 me；未登录/令牌失效则 account 为 null，
 * 界面显示登录页。登录、注册、绑定、退出都收敛在这里。
 */
export function useAccount() {
  const [account, setAccount] = useState<AccountResult | null>(null)
  const [ready, setReady] = useState(false)
  const [error, setError] = useState('')

  const bootstrap = useCallback(async () => {
    if (!getToken()) {
      setReady(true)
      return
    }
    setError('')
    try {
      setAccount(await authApi.me())
    } catch (e) {
      clearToken()
      setAccount(null)
      if (e instanceof Error && !e.message.includes('重新登录')) setError(e.message)
    } finally {
      setReady(true)
    }
  }, [])

  useEffect(() => { void bootstrap() }, [bootstrap])

  const login = useCallback(async (username: string, password: string) => {
    setAccount(await authApi.login(username, password))
  }, [])

  const register = useCallback(async (username: string, password: string) => {
    setAccount(await authApi.register(username, password))
  }, [])

  /** 绑定成功后就地更新绑定状态，聊天页随之加载共享会话。 */
  const bind = useCallback(async (code: string) => {
    const result = await authApi.bind(code)
    setAccount((current) => (current ? { ...current, binding: result.binding } : result))
  }, [])

  const logout = useCallback(() => {
    clearToken()
    setAccount(null)
  }, [])

  return { account, ready, error, reload: bootstrap, login, register, bind, logout }
}
