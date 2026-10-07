import assert from 'node:assert/strict'
import test from 'node:test'
import { wechatSubscriptionErrorMessage } from '../src/lib/wechatSubscriptionError'

test('native simulator rejection explains how to continue testing', () => {
  const result = wechatSubscriptionErrorMessage({
    errMsg: 'requestSubscribeMessage:fail 开发者工具暂时不支持此 API 调试，请使用真机进行开发',
  })
  assert.equal(result, '开发者工具暂不支持此订阅授权调试，请在手机微信中测试。')
})

test('native failures preserve a useful reason instead of disappearing behind instanceof Error', () => {
  assert.equal(wechatSubscriptionErrorMessage({ errMsg: 'requestSubscribeMessage:fail network error' }), '微信授权失败：network error')
  assert.match(wechatSubscriptionErrorMessage({ errMsg: 'requestSubscribeMessage:fail No template data return, verify the template id exist' }), /模板属于当前小程序/)
  assert.match(wechatSubscriptionErrorMessage({ errMsg: 'requestSubscribeMessage:fail The main switch is switched off' }), /设置中开启通知/)
})

test('backend failures keep their message and missing native details use the fallback', () => {
  assert.equal(wechatSubscriptionErrorMessage(new Error('暂时无法保存微信提醒设置，请稍后重试。')), '暂时无法保存微信提醒设置，请稍后重试。')
  for (const error of [undefined, null, {}, { errMsg: 42 }]) {
    assert.equal(wechatSubscriptionErrorMessage(error), '微信授权未完成，请再试一次。')
  }
})
