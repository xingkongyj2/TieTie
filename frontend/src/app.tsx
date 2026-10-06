import { useEffect, type PropsWithChildren } from 'react'
import { useDidHide, useDidShow } from '@tarojs/taro'
import { setAppVisible } from './lib/platform'
import { loadTitleFont } from './lib/fonts'
import { reminderLaunchStore } from './lib/reminderLaunch'
import './html-defaults.css'
import './styles.css'
import './mini.css'

export default function App({ children }: PropsWithChildren) {
  useEffect(() => { void loadTitleFont() }, [])
  useDidShow(options => {
    if (process.env.TARO_ENV === 'weapp' && options?.path?.replace(/^\//, '') === 'pages/index/index') {
      reminderLaunchStore.receive(options.query)
    }
    setAppVisible(true)
  })
  useDidHide(() => setAppVisible(false))
  return children
}
