import Taro from '@tarojs/taro'
import { resolveApiBaseURL } from '../api/origin'
export { resolveApiBaseURL } from '../api/origin'
declare const TARO_APP_API_BASE_URL: string

/** This is a public API origin, never a cloud credential. */
export function getApiBaseURL(): string {
  let environment = 'unknown'
  if (process.env.TARO_ENV === 'h5' && process.env.NODE_ENV !== 'production') environment = 'develop'
  else {
    try { environment = Taro.getAccountInfoSync().miniProgram.envVersion } catch { /* Fail closed outside development. */ }
  }
  const configured = typeof TARO_APP_API_BASE_URL === 'string' ? TARO_APP_API_BASE_URL : ''
  return resolveApiBaseURL(configured, environment)
}
export const apiBaseUrl = getApiBaseURL
export function apiURL(path: string): string {
  if (!path.startsWith('/api/')) throw new Error('接口路径无效。')
  return `${getApiBaseURL()}${path}`
}
