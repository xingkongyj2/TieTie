import { abortError, type AbortSignalLike } from '../lib/abort'

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS'
export interface RequestOptions {
  method?: HttpMethod
  headers?: Record<string, string>
  body?: unknown
  timeoutMs?: number
  signal?: AbortSignalLike
}
export class ApiError extends Error {
  constructor(message: string, public readonly status: number, public readonly code?: string) {
    super(message)
    this.name = 'ApiError'
  }
}
export function isAccountTokenInvalid(status: number, code?: string) {
  return status === 401 && code !== 'authentication_failed'
}
export interface NativeResponse { statusCode: number; data: unknown; header?: Record<string, unknown> }
export interface NativeRequestTask { abort(): void }
export interface NativeRequestOptions {
  url: string
  method: HttpMethod
  header: Record<string, string>
  data?: string
  timeout: number
  dataType: 'json'
  success(response: NativeResponse): void
  fail(error: { errMsg?: string }): void
}
export interface TransportDependencies {
  url(path: string): string
  getToken(): string | null
  invalidateToken(expected: string): void
  nativeRequest(options: NativeRequestOptions): NativeRequestTask
}
export function createTransport(deps: TransportDependencies) {
  return async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const url = deps.url(path)
    const { signal, timeoutMs = 30_000 } = options
    if (signal?.aborted) throw abortError()
    const token = deps.getToken()
    const headers: Record<string, string> = { Accept: 'application/json', ...options.headers }
    const hasHeader = (name: string) => Object.keys(headers).some(key => key.toLowerCase() === name.toLowerCase())
    if (token && !hasHeader('Authorization')) headers.Authorization = `Bearer ${token}`
    if (options.body !== undefined && !hasHeader('Content-Type')) headers['Content-Type'] = 'application/json'
    const method = options.method ?? 'GET'
    const data = options.body === undefined ? undefined : JSON.stringify(options.body)
    return new Promise<T>((resolve, reject) => {
      let task: NativeRequestTask | undefined
      let done = false
      let timer: ReturnType<typeof setTimeout> | undefined
      const finish = (error?: unknown, value?: T) => {
        if (done) return
        done = true
        clearTimeout(timer)
        signal?.removeEventListener('abort', onAbort)
        if (error) reject(error)
        else resolve(value as T)
      }
      const onAbort = () => { finish(abortError()); task?.abort() }
      const authAction = path === '/api/auth/login' ? '登录' : path === '/api/auth/register' ? '注册' : undefined
      const uncertain = method === 'POST' ? '消息可能已送达，请刷新确认后再发送。' : ''
      const authHint = authAction === '注册' ? '请检查网络和服务地址，也可尝试登录确认账号状态。' : '请检查网络和服务地址后重试。'
      const timeoutMessage = authAction ? `${authAction}请求超时，${authHint}` : `云端请求超时。${uncertain || '请刷新会话查看最新状态。'}`
      const networkMessage = authAction ? `${authAction}连接失败，${authHint}` : `连接云端失败，请检查网络和服务地址。${uncertain}`
      signal?.addEventListener('abort', onAbort, { once: true })
      timer = setTimeout(() => {
        finish(new ApiError(timeoutMessage, 0, 'TIMEOUT'))
        task?.abort()
      }, timeoutMs)
      try {
        task = deps.nativeRequest({ url, method, header: headers, data, timeout: timeoutMs, dataType: 'json',
          success(response) {
            if (done) return
            let payload = response.data
            if (typeof payload === 'string' && payload) {
              try { payload = JSON.parse(payload) } catch { /* Non-JSON errors use the status fallback. */ }
            }
            if (response.statusCode < 200 || response.statusCode >= 300) {
              const error = (payload as { error?: { message?: unknown; code?: unknown } } | null)?.error
              const code = typeof error?.code === 'string' ? error.code : undefined
              const message = typeof error?.message === 'string' ? error.message
                : response.statusCode === 401 ? '登录已过期，请重新登录。'
                : response.statusCode === 403 ? '没有访问这个空间的权限。'
                : response.statusCode === 409 ? '这个会话正在处理消息，请等当前回复结束。'
                : `云端请求失败，请稍后重试（${response.statusCode}）。`
              if (token && isAccountTokenInvalid(response.statusCode, code)) deps.invalidateToken(token)
              finish(new ApiError(message, response.statusCode, code))
            } else if (response.statusCode === 204) finish(undefined, undefined as T)
            else if (typeof payload === 'string') finish(new ApiError('云端返回格式无效，请重试。', response.statusCode, 'INVALID_RESPONSE'))
            else finish(undefined, payload as T)
          },
          fail(error) {
            if (done) return
            if (signal?.aborted) finish(abortError())
            else if (/timeout/i.test(error.errMsg ?? '')) finish(new ApiError(timeoutMessage, 0, 'TIMEOUT'))
            else finish(new ApiError(networkMessage, 0, 'NETWORK_ERROR'))
          },
        })
        if (done) task.abort()
      } catch (error) {
        finish(error instanceof ApiError ? error : new ApiError(networkMessage, 0, 'NETWORK_ERROR'))
      }
    })
  }
}
