import { readdir, readFile, writeFile, mkdir, copyFile, rm } from 'node:fs/promises'
import path from 'node:path'
import sharp from 'sharp'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import * as lucide from 'lucide-react'

const root = path.resolve(import.meta.dirname, '..')
const source = path.join(root, 'web/public')
const output = path.join(root, 'src/assets')
await mkdir(path.join(output, 'avatars'), { recursive: true })
await mkdir(path.join(output, 'ip/space-buddies-v1'), { recursive: true })
await mkdir(path.join(output, 'ip/paper-buddies-v2'), { recursive: true })
await mkdir(path.join(output, 'decor'), { recursive: true })
await mkdir(path.join(output, 'fonts'), { recursive: true })
// The Taro app only ships the small fallback set used when WeChat profile
// data is unavailable. Character-picker artwork stays in the legacy H5 tree
// and is intentionally excluded from the mini-program package.
const runtimeAvatars = new Set(['cream-cat.png', 'peach-cat.png', 'golden-longhair-cat.png', 'zodiac-rabbit.png', 'corgi-dog.png', 'otter.png', 'penguin.png'])
for (const name of (await readdir(path.join(source, 'avatars'))).filter(name => name.endsWith('.png') && runtimeAvatars.has(name))) {
  await sharp(path.join(source, 'avatars', name)).resize(240, 240, { fit: 'inside', withoutEnlargement: true }).png({ compressionLevel: 9 }).toFile(path.join(output, 'avatars', name))
}
await sharp(path.join(source, 'brand-notes.png')).resize(180, 180, { fit: 'inside' }).png({ compressionLevel: 9 }).toFile(path.join(output, 'brand-notes.png'))
for (const name of ['blue-buddy', 'ice-buddy']) {
  await sharp(path.join(source, 'ip/space-buddies-v1', `${name}.svg`), { density: 192 }).resize({ width: 360 }).png({ compressionLevel: 9 }).toFile(path.join(output, 'ip/space-buddies-v1', `${name}.png`))
}
// Animation artwork loads remotely; remove stale copies from older builds.
for (const name of ['blue-buddy.png', 'ice-buddy.png']) {
  await rm(path.join(output, 'ip/paper-buddies-v2', name), { force: true })
}
for (const name of ['duo.png', 'blue-buddy-avatar-256.png']) {
  await copyFile(path.join(source, 'ip/paper-buddies-v2', name), path.join(output, 'ip/paper-buddies-v2', name))
}
// Top-card artwork renders at 144px; a 3x PNG keeps it sharp within the package budget.
await sharp(path.join(source, 'ip/paper-buddies-v2/todo-duo.png')).resize({ width: 432, withoutEnlargement: true }).png({ compressionLevel: 9 }).toFile(path.join(output, 'ip/paper-buddies-v2/todo-duo.png'))
for (const name of ['reminder-titles-b1adf8b8db9d.woff2', 'OFL.txt']) await copyFile(path.join(source, 'fonts', name), path.join(output, 'fonts', name))

const svgs = {
  'pair-decor': '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 210 170"><ellipse cx="111" cy="144" rx="87" ry="15" fill="none" stroke="#B7D3F2" stroke-dasharray="3 6" transform="rotate(-16 111 144)"/><path d="M46 14L50 27L63 31L50 35L46 48L42 35L29 31L42 27L46 14Z" fill="#F1D58D" fill-opacity=".8"/><path d="M186 37V47M181 42H191" stroke="#EAB7CB" stroke-width="2" stroke-linecap="round"/><circle cx="17" cy="118" r="3" fill="#B8DBFF"/></svg>',
  'loading-ring': '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200"><circle cx="100" cy="100" r="80" fill="none" stroke="#cddff8" stroke-width="3" stroke-linecap="round" stroke-dasharray="0.5 15"/></svg>',
  'loading-spark': '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M12 1.6 L14.1 9.9 L22.4 12 L14.1 14.1 L12 22.4 L9.9 14.1 L1.6 12 L9.9 9.9 Z" fill="#f1d58d"/></svg>',
  'loading-spark-pink': '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M12 1.6 L14.1 9.9 L22.4 12 L14.1 14.1 L12 22.4 L9.9 14.1 L1.6 12 L9.9 9.9 Z" fill="#eab7cb"/></svg>',
  'loading-spark-blue': '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M12 1.6 L14.1 9.9 L22.4 12 L14.1 14.1 L12 22.4 L9.9 14.1 L1.6 12 L9.9 9.9 Z" fill="#a8c8f0"/></svg>',
  'things-decor-1': '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 144 144" fill="none"><path d="M124 10V26M116 18H132" stroke="#93BCF0" stroke-width="2.5" stroke-linecap="round"/></svg>',
  'things-decor-2': '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 144 144" fill="none"><path d="M8 14V22M4 18H12" stroke="#EAB7CB" stroke-width="1.8" stroke-linecap="round"/></svg>',
  'things-decor-3': '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 144 144" fill="none"><path d="M14 115L17 122L24 125L17 128L14 135L11 128L4 125L11 122Z" fill="#F1D58D" fill-opacity=".8"/></svg>',
}
for (const [name, svg] of Object.entries(svgs)) await sharp(Buffer.from(svg), { density: 192 }).png({ compressionLevel: 9 }).toFile(path.join(output, 'decor', `${name}.png`))

// 直接从原 Lucide 几何生成 SVG 遮罩。currentColor 保留所有父级颜色与动画。
const names = new Set(['CircleHelp'])
const files = [path.join(root, 'web/src/App.tsx'), ...(await readdir(path.join(root, 'web/src/components'))).filter(name => name.endsWith('.tsx')).map(name => path.join(root, 'web/src/components', name))]
for (const file of files) {
  const text = await readFile(file, 'utf8')
  for (const match of text.matchAll(/import\s*\{([^}]+)\}\s*from\s*['"]lucide-react['"]/g)) for (const entry of match[1].split(',')) names.add(entry.trim().split(/\s+as\s+/)[0])
}
const icons = {}
for (const name of [...names].sort()) {
  if (!lucide[name]) throw new Error(`Unknown icon: ${name}`)
  icons[name] = renderToStaticMarkup(React.createElement(lucide[name], { size: 24, color: '#000', strokeWidth: 2 })).replace(/ class="[^"]*"/, '')
}
await writeFile(path.join(root, 'src/components/icons-data.json'), JSON.stringify(icons))
console.log(`Prepared ${names.size} vector icons and original artwork.`)
