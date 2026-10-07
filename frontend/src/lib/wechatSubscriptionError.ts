/** WeChat rejects with { errMsg, errCode }, which is usually not an Error. */
export function wechatSubscriptionErrorMessage(error: unknown): string {
  const native = error && typeof error === 'object' ? error as { errMsg?: unknown; message?: unknown } : null
  const message = typeof native?.errMsg === 'string' ? native.errMsg
    : typeof native?.message === 'string' ? native.message : ''
  if (/(开发者工具|devtools|simulator)/i.test(message) && /(不支持|not support|unsupported)/i.test(message)) {
    return '开发者工具暂不支持此订阅授权调试，请在手机微信中测试。'
  }
  if (/main switch is switched off/i.test(message)) {
    return '微信订阅消息开关已关闭，请在小程序设置中开启通知后重试。'
  }
  if (/no template data return|invalid template id/i.test(message)) {
    return '微信未找到订阅模板，请确认模板属于当前小程序。'
  }
  if (error instanceof Error && message) return message
  const detail = message.replace(/^requestSubscribeMessage:fail\s*/i, '').trim()
  return detail ? `微信授权失败：${detail.slice(0, 160)}` : '微信授权未完成，请再试一次。'
}
