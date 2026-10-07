export default defineAppConfig({
  pages: ['pages/index/index'],
  window: {
    navigationStyle: 'custom', navigationBarTitleText: '贴贴清单', navigationBarTextStyle: 'black',
    backgroundColor: '#ffffff', backgroundTextStyle: 'dark',
  },
  networkTimeout: { request: 120000 },
  ...(process.env.TARO_ENV === 'weapp' ? {
    ...(process.env.TARO_APP_WECHAT_SI_ENABLED === 'true' ? {
      plugins: { WechatSI: { version: '0.3.10', provider: 'wx069ba97219f66d99' } },
    } : {}),
    permission: { 'scope.record': { desc: '将你主动录制的语音转换成可编辑的聊天文字' } },
  } : {}),
})
