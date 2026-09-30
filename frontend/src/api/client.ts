/** Same-origin HTTP adapter. Cloud credentials stay in the server. */
import { clearToken, getToken } from '../lib/token'

export interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: unknown
  timeoutMs?: number
}

export class ApiError extends Error {
  constructor(message: string, public readonly status: number, public readonly code?: string) {
    super(message)
    this.name = 'ApiError'
  }
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { body, timeoutMs = 30_000, signal, ...fetchOptions } = options
  const controller = new AbortController()
  let timedOut = false
  const forwardAbort = () => controller.abort()
  if (signal?.aborted) controller.abort()
  else signal?.addEventListener('abort', forwardAbort, { once: true })
  const timeout = setTimeout(() => { timedOut = true; controller.abort() }, timeoutMs)

  try {
    const headers = new Headers(fetchOptions.headers)
    headers.set('Accept', 'application/json')
    const token = getToken()
    if (token && !headers.has('Authorization')) {
      headers.set('Authorization', `Bearer ${token}`)
    }
    if (body !== undefined && !headers.has('Content-Type')) {
      headers.set('Content-Type', 'application/json')
    }
    const response = await fetch(path, {
      ...fetchOptions,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal,
    })
    if (!response.ok) {
      // 令牌失效：清掉本地 JWT，界面随账号状态回到登录页。
      if (response.status === 401 && getToken()) clearToken()
      const payload = await response.json().catch(() => null) as { error?: { message?: unknown; code?: unknown } } | null
      const fallback = response.status === 401 || response.status === 403
        ? '云端认证失败，请检查服务端令牌和访问权限。'
        : response.status === 409
          ? '这个会话正在处理消息，请等当前回复结束。'
          : `云端请求失败，请稍后重试（${response.status}）。`
      const message = typeof payload?.error?.message === 'string' ? payload.error.message : fallback
      const code = typeof payload?.error?.code === 'string' ? payload.error.code : undefined
      throw new ApiError(message, response.status, code)
    }
    if (response.status === 204) return undefined as T
    return await response.json() as T
  } catch (error) {
    if (signal?.aborted) throw error
    const sendUncertainty = fetchOptions.method?.toUpperCase() === 'POST'
      ? '消息可能已送达，请刷新确认后再发送。'
      : ''
    if (timedOut) throw new ApiError(`云端请求超时。${sendUncertainty || '请刷新会话查看最新状态。'}`, 0, 'TIMEOUT')
    if (error instanceof ApiError) throw error
    if (error instanceof TypeError) throw new ApiError(`连接云端失败，请检查网络和本地服务。${sendUncertainty}`, 0, 'NETWORK_ERROR')
    throw error
  } finally {
    clearTimeout(timeout)
    signal?.removeEventListener('abort', forwardAbort)
  }
}
