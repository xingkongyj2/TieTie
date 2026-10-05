import { useEffect, type PropsWithChildren } from 'react'
import { useDidHide, useDidShow } from '@tarojs/taro'
import { setAppVisible } from './lib/platform'
import { loadTitleFont } from './lib/fonts'
import './html-defaults.css'
import './styles.css'
import './mini.css'

export default function App({ children }: PropsWithChildren) {
  useEffect(() => { void loadTitleFont() }, [])
  useDidShow(() => setAppVisible(true))
  useDidHide(() => setAppVisible(false))
  return children
}
