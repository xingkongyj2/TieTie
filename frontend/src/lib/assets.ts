/** 数据库存储原相对头像名，显示时映射到经过尺寸优化的小程序资源。 */
export function assetUrl(src: string): string {
  if (src.startsWith('/avatars/')) return `/assets${src}`
  if (src.startsWith('/ip/space-buddies-v1/')) return `/assets${src.replace(/\.svg$/, '.png')}`
  if (src === '/brand-notes.png') return '/assets/brand-notes.png'
  return src
}
