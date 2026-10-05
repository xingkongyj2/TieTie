/** Resolve public backend configuration without guessing a deployed domain. */
export function resolveApiBaseURL(configured: string, environment: string): string {
  const origin = configured.trim().replace(/\/+$/, '')
  if (origin) {
    if (!/^https?:\/\/(?:\[[\da-f:]+\]|[a-z\d](?:[a-z\d.-]*[a-z\d])?)(?::\d{1,5})?(?:\/[^\s?#]*)?$/i.test(origin)) throw new Error('API 地址配置无效，请填写完整的 HTTP(S) 地址。')
    if (environment !== 'develop' && !/^https:\/\//i.test(origin)) throw new Error('体验版和正式版请配置 HTTPS API 地址。')
    return origin
  }
  if (environment === 'develop') return 'http://127.0.0.1:4173'
  throw new Error('尚未配置服务地址，请设置 TARO_APP_API_BASE_URL 后重新构建。')
}
