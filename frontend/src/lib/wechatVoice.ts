import Taro from '@tarojs/taro'
import type { RecognitionManager } from './voiceRecognition'
import { getTencentRecognitionManager } from './tencentVoice'

interface NativeCallbacks<T> {
  success: (result: T) => void
  fail: (error: unknown) => void
}

interface VoiceSetting {
  authSetting: { 'scope.record'?: boolean }
}

export interface WechatVoiceDependencies {
  isWeapp: () => boolean
  isEnabled: () => boolean
  requirePlugin: (name: string) => unknown
  getRecognitionManager?: () => RecognitionManager
  getPrivacySetting?: (options: NativeCallbacks<{ needAuthorization: boolean }>) => unknown
  requirePrivacyAuthorize?: (options: NativeCallbacks<unknown>) => unknown
  getSetting: (options: NativeCallbacks<VoiceSetting>) => unknown
  authorize: (options: NativeCallbacks<unknown> & { scope: 'scope.record' }) => unknown
  showModal: (options: NativeCallbacks<{ confirm: boolean }> & {
    title: string
    content: string
    confirmText: string
    cancelText: string
  }) => unknown
  openSetting: (options: NativeCallbacks<VoiceSetting>) => unknown
}

const unsupportedMessage = '请在微信小程序中使用语音输入。'
const permissionMessage = '麦克风权限未开启，请开启录音权限后再试，也可以继续打字。'
const unavailableMessage = '语音输入暂不可用，请先用文字输入。'

/** Accept callback-only WeChat APIs as well as Taro's Promise wrappers. */
function nativeCall<T>(invoke: (options: NativeCallbacks<T>) => unknown, message: string): Promise<T> {
  return new Promise((resolve, reject) => {
    const fail = () => reject(new Error(message))
    try {
      const result = invoke({ success: resolve, fail })
      if (result && typeof (result as PromiseLike<T>).then === 'function') {
        void Promise.resolve(result as PromiseLike<T>).then(resolve, fail)
      }
    } catch {
      fail()
    }
  })
}

export function createWechatVoiceAdapter(dependencies: WechatVoiceDependencies) {
  let manager: RecognitionManager | undefined
  let pendingAuthorization: Promise<boolean> | undefined

  function ensureAvailable() {
    if (!dependencies.isWeapp()) throw new Error(unsupportedMessage)
    if (!dependencies.isEnabled()) throw new Error(unavailableMessage)
  }

  function getWechatRecognitionManager(): RecognitionManager {
    ensureAvailable()
    if (manager) return manager
    try {
      if (dependencies.getRecognitionManager) {
        manager = dependencies.getRecognitionManager()
        return manager
      }
      const plugin = dependencies.requirePlugin('WechatSI') as {
        getRecordRecognitionManager?: () => RecognitionManager
      } | undefined
      if (typeof plugin?.getRecordRecognitionManager !== 'function') throw new Error()
      const candidate = plugin.getRecordRecognitionManager()
      if (!candidate || typeof candidate.start !== 'function' || typeof candidate.stop !== 'function') throw new Error()
      manager = candidate
      return candidate
    } catch {
      throw new Error('语音服务暂不可用，请稍后再试。')
    }
  }

  function requestMicrophoneSetting(): Promise<boolean> {
    // Keep openSetting inside the modal's success callback: WeChat requires
    // an actual user tap before opening its permission settings screen.
    return new Promise((resolve, reject) => {
      const fail = () => reject(new Error(permissionMessage))
      try {
        const result = dependencies.showModal({
          title: '需要麦克风权限',
          content: '语音转文字需要使用麦克风，请在设置中开启录音权限。你也可以继续打字。',
          confirmText: '去设置',
          cancelText: '暂不开启',
          success: ({ confirm }) => {
            if (!confirm) { fail(); return }
            void nativeCall<VoiceSetting>(options => dependencies.openSetting(options), '暂时无法打开微信设置，请稍后再试。')
              .then(setting => {
                if (setting.authSetting['scope.record'] === true) resolve(false)
                else fail()
              }, reject)
          },
          fail,
        })
        // The callback above handles success synchronously. Only consume a
        // returned rejection here to avoid an unhandled Taro Promise rejection.
        if (result && typeof (result as PromiseLike<unknown>).then === 'function') {
          void Promise.resolve(result).catch(fail)
        }
      } catch {
        fail()
      }
    })
  }

  async function requestAuthorization(): Promise<boolean> {
    ensureAvailable()
    let showedPrivacyPrompt = false
    if (dependencies.getPrivacySetting) {
      const privacy = await nativeCall<{ needAuthorization: boolean }>(
        options => dependencies.getPrivacySetting!(options),
        '暂时无法确认隐私授权，请稍后再试。',
      )
      if (privacy.needAuthorization) {
        if (!dependencies.requirePrivacyAuthorize) throw new Error('请更新微信后再使用语音输入。')
        await nativeCall<unknown>(
          options => dependencies.requirePrivacyAuthorize!(options),
          '同意隐私保护指引后才能使用语音输入，也可以继续打字。',
        )
        showedPrivacyPrompt = true
      }
    }

    const setting = await nativeCall<VoiceSetting>(
      options => dependencies.getSetting(options),
      '暂时无法读取麦克风权限，请稍后再试。',
    )
    if (setting.authSetting['scope.record'] === true) return !showedPrivacyPrompt
    if (setting.authSetting['scope.record'] === false) return requestMicrophoneSetting()

    try {
      await nativeCall<unknown>(
        options => dependencies.authorize({ ...options, scope: 'scope.record' }),
        permissionMessage,
      )
      // Touch-end may have happened behind the permission dialog. A fresh
      // press is always required; completing authorization never starts audio.
      return false
    } catch {
      return requestMicrophoneSetting()
    }
  }

  function authorizeWechatVoice(): Promise<boolean> {
    if (!pendingAuthorization) {
      pendingAuthorization = requestAuthorization().finally(() => { pendingAuthorization = undefined })
    }
    return pendingAuthorization
  }

  return { getWechatRecognitionManager, authorizeWechatVoice }
}

const adapter = createWechatVoiceAdapter({
  isWeapp: () => process.env.TARO_ENV === 'weapp',
  isEnabled: () => true,
  requirePlugin: name => Taro.requirePlugin(name),
  getRecognitionManager: process.env.TARO_APP_WECHAT_SI_ENABLED === 'true' ? undefined : getTencentRecognitionManager,
  getPrivacySetting: typeof Taro.getPrivacySetting === 'function' ? options => Taro.getPrivacySetting(options) : undefined,
  requirePrivacyAuthorize: typeof Taro.requirePrivacyAuthorize === 'function' ? options => Taro.requirePrivacyAuthorize(options) : undefined,
  getSetting: options => Taro.getSetting(options),
  authorize: options => Taro.authorize(options),
  showModal: options => Taro.showModal(options),
  openSetting: options => Taro.openSetting(options),
})

export const getWechatRecognitionManager = adapter.getWechatRecognitionManager
export const authorizeWechatVoice = adapter.authorizeWechatVoice
