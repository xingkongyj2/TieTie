import { defineConfig } from '@tarojs/cli'
import path from 'node:path'

export default defineConfig({
  projectName: 'tietie-miniprogram', date: '2026-10-05',
  designWidth: 375, deviceRatio: { 375: 2, 750: 1 },
  sourceRoot: 'src', outputRoot: process.env.TARO_ENV === 'h5' ? 'dist-h5' : 'dist',
  framework: 'react', compiler: { type: 'webpack5', prebundle: { enable: false } },
  plugins: [['@tarojs/plugin-html', {
    modifyElements(_inline: string[], block: string[]) {
      block.push('summary', 'thead', 'tbody', 'tfoot', 'tr', 'td', 'th', 'caption', 'colgroup', 'col')
    },
  }]],
  defineConstants: { TARO_APP_API_BASE_URL: JSON.stringify(process.env.TARO_APP_API_BASE_URL || '') },
  copy: {
    patterns: [{ from: 'src/assets', to: process.env.TARO_ENV === 'h5' ? 'dist-h5/assets' : 'dist/assets' }],
    options: {},
  },
  mini: {
    // 第三方依赖也需要转译：真机不支持未转换的 ?? 和 Unicode 属性正则。
    compile: {
      include: ['xls-reader', 'estree-util-is-identifier-name', 'micromark-util-character']
        .map((name) => path.resolve(__dirname, '../node_modules', name)),
    },
    postcss: {
      // 保留 H5 逻辑像素，避免自动 px→rpx 改变字体与间距。
      pxtransform: { enable: false },
      url: { enable: true, config: { limit: 8192 } },
      cssModules: { enable: false },
      './scripts/postcss-weapp-selectors.cjs': { enable: true },
    },
  },
  h5: {
    publicPath: '/', staticDirectory: 'static',
    devServer: { host: '127.0.0.1', port: 5174, proxy: [{ context: ['/api'], target: 'http://127.0.0.1:4173' }] },
    postcss: {
      pxtransform: { enable: false }, cssModules: { enable: false },
      htmltransform: { enable: false },
      './scripts/postcss-h5-tags.cjs': { enable: true },
    },
  },
})
