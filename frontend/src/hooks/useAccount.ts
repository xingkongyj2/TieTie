import { useCallback, useEffect, useState } from 'react'
import { authApi, type AccountResult } from '../api/auth'
import { clearToken, getToken } from '../lib/token'

type OnboardingStep = 'profile' | 'bind' | 'usage'
const onboardingKey = (userId: number) => `tietie.onboarding.${userId}`
const readOnboarding = (userId: number): OnboardingStep | null => {
  try {
    const value = localStorage.getItem(onboardingKey(userId))
    return value === 'profile' || value === 'bind' || value === 'usage' ? value : null
  } catch { return null }
}
const writeOnboarding = (userId: number, step: OnboardingStep | null) => {
  try {
    if (step) localStorage.setItem(onboardingKey(userId), step)
    else localStorage.removeItem(onboardingKey(userId))
  } catch { /* Onboarding still works for this session when storage is unavailable. */ }
}

/**
 * 账号状态：启动时用本地 JWT 拉取 me；未登录/令牌失效则 account 为 null，
 * 界面显示登录页。登录、注册、绑定、解绑、退出都收敛在这里。
 */
export function useAccount() {
  const [account, setAccount] = useState<AccountResult | null>(null)
  const [ready, setReady] = useState(false)
  const [error, setError] = useState('')
  const [onboardingStep, setOnboardingStep] = useState<OnboardingStep | null>(null)

  const bootstrap = useCallback(async () => {
    if (!getToken()) {
      setReady(true)
      return
    }
    setError('')
    try {
      const result = await authApi.me()
      setOnboardingStep(readOnboarding(result.user.userId))
      setAccount(result)
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
    const result = await authApi.login(username, password)
    setOnboardingStep(readOnboarding(result.user.userId))
    setAccount(result)
  }, [])

  const register = useCallback(async (username: string, password: string) => {
    const result = await authApi.register(username, password)
    writeOnboarding(result.user.userId, 'profile')
    setOnboardingStep('profile')
    setAccount(result)
  }, [])

  const advanceOnboarding = useCallback(() => {
    if (!account || !onboardingStep || onboardingStep === 'usage') return
    const next = onboardingStep === 'profile' ? 'bind' : 'usage'
    writeOnboarding(account.user.userId, next)
    setOnboardingStep(next)
  }, [account, onboardingStep])

  const finishOnboarding = useCallback(() => {
    if (!account) return
    writeOnboarding(account.user.userId, null)
    setOnboardingStep(null)
  }, [account])

  /** 绑定成功后就地更新绑定状态，聊天页随之加载共享会话。 */
  const bind = useCallback(async (code: string) => {
    const result = await authApi.bind(code)
    setAccount((current) => (current ? { ...current, binding: result.binding } : result))
  }, [])

  /** 退出当前会话：后端解绑后绑定状态清空，聊天页回到绑定引导页。 */
  const unbind = useCallback(async () => {
    const result = await authApi.unbind()
    setAccount((current) => (current ? { ...current, binding: result.binding } : result))
  }, [])

  const logout = useCallback(() => {
    clearToken()
    setAccount(null)
    setOnboardingStep(null)
  }, [])

  return { account, ready, error, onboardingStep, advanceOnboarding, finishOnboarding, reload: bootstrap, login, register, bind, unbind, logout }
}
