import { useLoad, useShareAppMessage } from '@tarojs/taro'
import Taro from '@tarojs/taro'
import { View } from '@tarojs/components'
import type { CSSProperties } from 'react'
import TieTieApp from '../../TieTieApp'
import { reminderLaunchStore } from '../../lib/reminderLaunch'

export default function Index() {
  let nativeStyle: CSSProperties | undefined
  if (process.env.TARO_ENV === 'weapp') {
    const info = Taro.getWindowInfo()
    const capsule = Taro.getMenuButtonBoundingClientRect()
    const top = Math.max(capsule?.bottom || (info.statusBarHeight || 0) + 44, 7) + 4
    const capsuleTop = capsule?.top > 0 ? capsule.top : (info.statusBarHeight || 0) + 6
    const capsuleHeight = capsule?.height > 0 ? capsule.height : 32
    const capsuleLeft = capsule?.left > 0 ? capsule.left : (info.windowWidth || 375) - 100
    nativeStyle = {
      '--mini-nav-top': `${top}px`,
      '--mini-capsule-top': `${capsuleTop}px`,
      '--mini-capsule-height': `${capsuleHeight}px`,
      '--mini-capsule-left': `${capsuleLeft}px`,
    } as CSSProperties
  }
  useLoad(options => {
    if (/^\d{4}$/.test(options.invite || '')) Taro.setStorageSync('tietie.pendingInvite', options.invite)
    if (process.env.TARO_ENV === 'weapp' && !reminderLaunchStore.get()) reminderLaunchStore.receive(options)
  })
  useShareAppMessage(() => {
    const code = Taro.getStorageSync<string>('tietie.inviteCode')
    return { title: '和我一起，把日常放进贴贴清单', path: `/pages/index/index${/^\d{4}$/.test(code) ? `?invite=${code}` : ''}` }
  })
  return <View className="mini-root" style={nativeStyle}><TieTieApp /></View>
}
