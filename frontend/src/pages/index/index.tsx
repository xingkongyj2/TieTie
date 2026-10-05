import { useLoad, useShareAppMessage } from '@tarojs/taro'
import Taro from '@tarojs/taro'
import { View } from '@tarojs/components'
import type { CSSProperties } from 'react'
import TieTieApp from '../../TieTieApp'

export default function Index() {
  let nativeStyle: CSSProperties | undefined
  if (process.env.TARO_ENV === 'weapp') {
    const info = Taro.getWindowInfo()
    const capsule = Taro.getMenuButtonBoundingClientRect()
    const top = Math.max(capsule?.bottom || (info.statusBarHeight || 0) + 44, 7) + 4
    nativeStyle = { '--mini-nav-top': `${top}px` } as CSSProperties
  }
  useLoad(options => {
    if (/^\d{4}$/.test(options.invite || '')) Taro.setStorageSync('tietie.pendingInvite', options.invite)
  })
  useShareAppMessage(() => {
    const code = Taro.getStorageSync<string>('tietie.inviteCode')
    return { title: '和我一起，把日常放进贴贴清单', path: `/pages/index/index${/^\d{4}$/.test(code) ? `?invite=${code}` : ''}` }
  })
  return <View className="mini-root" style={nativeStyle}><TieTieApp /></View>
}
