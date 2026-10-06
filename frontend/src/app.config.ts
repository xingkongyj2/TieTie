export default defineAppConfig({
  pages: ['pages/index/index'],
  window: {
    navigationStyle: 'custom', navigationBarTitleText: '贴贴清单',
    backgroundColor: '#ffffff', backgroundTextStyle: 'dark',
  },
  networkTimeout: { request: 120000 },
})
