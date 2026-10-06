import assert from 'node:assert/strict'
import test from 'node:test'
import { completeWechatLogin } from '../src/api/wechat-login'

test('WeChat login exchanges a fresh code on each attempt and returns the backend account', async () => {
  let attempts = 0
  const exchanged: string[] = []
  const account = { token: 'account-jwt', user: { userId: 1 }, isNewUser: true }
  const dependencies = {
    login: async () => ({ code: `code-${++attempts}` }),
    exchange: async (code: string) => { exchanged.push(code); return account },
  }
  assert.equal(await completeWechatLogin(dependencies), account)
  assert.equal(await completeWechatLogin(dependencies), account)
  assert.deepEqual(exchanged, ['code-1', 'code-2'])
})

test('optional WeChat profile data is forwarded without becoming login proof', async () => {
  let received: { code: string; profile?: { nickname: string; avatarUrl: string } } | undefined
  const account = { token: 'account-jwt' }
  const result = await completeWechatLogin({
    getProfile: async () => ({ nickname: '小贴', avatarUrl: 'https://thirdwx.qlogo.cn/avatar' }),
    login: async () => ({ code: 'fresh-code' }),
    exchange: async (code, profile) => { received = { code, profile }; return account },
  })
  assert.equal(result, account)
  assert.deepEqual(received, { code: 'fresh-code', profile: { nickname: '小贴', avatarUrl: 'https://thirdwx.qlogo.cn/avatar' } })
})

test('declining optional WeChat profile still completes login', async () => {
  let exchanged = false
  const account = { token: 'account-jwt' }
  const result = await completeWechatLogin({
    getProfile: async () => { throw new Error('user denied') },
    login: async () => ({ code: 'fresh-code' }),
    exchange: async (code, profile) => { exchanged = code === 'fresh-code' && profile === undefined; return account },
  })
  assert.equal(result, account)
  assert.equal(exchanged, true)
})

test('missing WeChat code does not call the backend', async () => {
  let exchanged = false
  await assert.rejects(completeWechatLogin({
    login: async () => ({ code: ' ' }),
    exchange: async () => { exchanged = true },
  }), /未获取到微信登录凭证/)
  assert.equal(exchanged, false)
})

test('native login failure stays retryable and does not leak native error details', async () => {
  let exchanged = false
  await assert.rejects(completeWechatLogin({
    login: async () => { throw { errMsg: 'login:fail internal details' } },
    exchange: async () => { exchanged = true },
  }), { message: '暂时无法连接微信，请稍后重试。' })
  assert.equal(exchanged, false)
})

test('backend login errors reach the login page without an automatic code retry', async () => {
  let attempts = 0
  const error = new Error('微信登录暂不可用，请稍后重试。')
  await assert.rejects(completeWechatLogin({
    login: async () => { attempts++; return { code: 'fresh-code' } },
    exchange: async () => { throw error },
  }), e => e === error)
  assert.equal(attempts, 1)
})
