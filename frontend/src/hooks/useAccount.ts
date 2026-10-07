import Taro from '@tarojs/taro'
import { useCallback, useEffect, useRef, useState } from 'react'
import { authApi, type AccountResult } from '../api/auth'
import type { WechatLoginProfile } from '../api/wechat-login'
import { clearToken, getToken, onAccountTokenInvalid, setToken } from '../lib/token'
import { clearImagePreviewCache } from '../lib/files'
import { ApiError, isAccountTokenInvalid } from '../api/client'
import { readStorage, writeStorage, removeStorage } from '../lib/storage'

type OnboardingStep = 'profile' | 'bind' | 'usage'
const onboardingKey = (userId: number) => `tietie.onboarding.${userId}`
const inviteKey = 'tietie.inviteCode'
const saveInvite = (account: AccountResult | null) => {
  if (!account?.user.code) { removeStorage(inviteKey); return }
  // The page's share callback reads a raw string using Taro.getStorageSync.
  try { Taro.setStorageSync(inviteKey, account.user.code) }
  catch { removeStorage(inviteKey) }
}
const readOnboarding = (userId: number): OnboardingStep | null => {
  try {
    const value = readStorage<string | null>(onboardingKey(userId), () => null)
    return value === 'profile' || value === 'bind' || value === 'usage' ? value : null
  } catch { return null }
}
const writeOnboarding = (userId: number, step: OnboardingStep | null) => {
  try {
    if (step) writeStorage(onboardingKey(userId), step)
    else removeStorage(onboardingKey(userId))
  } catch { /* Onboarding still works for this session when storage is unavailable. */ }
}

/**
 * 账号状态：启动时用本地 JWT 拉取 me；未登录/令牌失效则 account 为 null，
 * 界面显示登录页。登录、注册、绑定、解绑、退出都收敛在这里。
 */
export function useAccount() {
  const sequence = useRef(0)
  const [account, setAccount] = useState<AccountResult | null>(null)
  const [ready, setReady] = useState(false)
  const [error, setError] = useState('')
  const [onboardingStep, setOnboardingStep] = useState<OnboardingStep | null>(null)

  const bootstrap = useCallback(async () => {
    const generation = ++sequence.current
    if (!getToken()) {
      saveInvite(null)
      setAccount(null)
      setOnboardingStep(null)
      setReady(true)
      return
    }
    setError('')
    try {
      const result = await authApi.me()
      if (generation !== sequence.current) return
      saveInvite(result)
      setOnboardingStep(readOnboarding(result.user.userId))
      setAccount(result)
      setReady(true)
    } catch (e) {
      if (generation !== sequence.current) return
      if (e instanceof ApiError && isAccountTokenInvalid(e.status, e.code)) {
        clearToken(); saveInvite(null); void clearImagePreviewCache()
        setAccount(null); setOnboardingStep(null); setReady(true)
        setError('登录已过期，请重新登录。')
      } else {
        setError(e instanceof Error ? e.message : '暂时无法连接云端，请重试。')
        setReady(false)
      }
    }
  }, [])

  useEffect(() => {
    const dispose = onAccountTokenInvalid(() => {
      sequence.current++
      saveInvite(null); void clearImagePreviewCache()
      setAccount(null); setOnboardingStep(null); setReady(true)
      setError('登录已过期，请重新登录。')
      void Taro.showToast({ title: '登录已过期，请重新登录', icon: 'none' }).catch(() => {})
    })
    // Remove the previous account's share code before the first /me response.
    saveInvite(null)
    void clearImagePreviewCache()
    void bootstrap()
    return () => { sequence.current++; dispose() }
  }, [bootstrap])

  const login = useCallback(async (username: string, password: string) => {
    const generation = ++sequence.current
    const result = await authApi.login(username, password)
    if (generation !== sequence.current) return
    if (result.token) setToken(result.token)
    saveInvite(result)
    void clearImagePreviewCache()
    setError(''); setReady(true)
    setOnboardingStep(readOnboarding(result.user.userId))
    setAccount(result)
  }, [])

  const register = useCallback(async (username: string, password: string) => {
    const generation = ++sequence.current
    const result = await authApi.register(username, password)
    if (generation !== sequence.current) return
    if (result.token) setToken(result.token)
    saveInvite(result)
    void clearImagePreviewCache()
    setError(''); setReady(true)
    writeOnboarding(result.user.userId, 'profile')
    setOnboardingStep('profile')
    setAccount(result)
  }, [])

  const wechatLogin = useCallback(async (profile?: WechatLoginProfile) => {
    const generation = ++sequence.current
    const result = await authApi.wechatLogin(profile)
    if (generation !== sequence.current) return
    if (result.token) setToken(result.token)
    saveInvite(result)
    void clearImagePreviewCache()
    setError(''); setReady(true)
    if (result.isNewUser || result.needsProfileSetup) {
      writeOnboarding(result.user.userId, 'profile')
      setOnboardingStep('profile')
    } else setOnboardingStep(readOnboarding(result.user.userId))
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
    const generation = sequence.current
    const result = await authApi.bind(code)
    if (generation !== sequence.current) return
    saveInvite(result)
    setAccount((current) => (current ? { ...current, binding: result.binding } : result))
  }, [])

  /** 退出当前会话：后端解绑后绑定状态清空，聊天页回到绑定引导页。 */
  const unbind = useCallback(async () => {
    const generation = sequence.current
    const result = await authApi.unbind()
    if (generation !== sequence.current) return
    saveInvite(result)
    setAccount((current) => (current ? { ...current, binding: result.binding } : result))
  }, [])

  const logout = useCallback(() => {
    sequence.current++
    clearToken()
    saveInvite(null)
    void clearImagePreviewCache()
    setAccount(null)
    setOnboardingStep(null)
    setError('')
    setReady(true)
  }, [])

  return { account, ready, error, onboardingStep, advanceOnboarding, finishOnboarding, reload: bootstrap, login, register, wechatLogin, bind, unbind, logout }
}
