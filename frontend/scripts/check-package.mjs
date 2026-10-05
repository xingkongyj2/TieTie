import { readdir, stat, readFile } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import path from 'node:path'
const root = path.resolve(import.meta.dirname, '../dist')
const styles = []
async function bytes(directory) {
  let total = 0
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name)
    if (entry.isFile() && file.endsWith('.wxss')) styles.push(file)
    total += entry.isDirectory() ? await bytes(file) : (await stat(file)).size
  }
  return total
}
const app = JSON.parse(await readFile(path.join(root, 'app.json'), 'utf8'))
for (const page of app.pages) for (const suffix of ['js', 'json', 'wxml', 'wxss']) await stat(path.join(root, `${page}.${suffix}`))
const size = await bytes(root)
if (size > 2 * 1024 * 1024) throw new Error(`小程序主包 ${(size / 1024).toFixed(0)} KiB 超过 2 MiB，请调整资源或分包。`)
const compiler = process.env.WECHAT_WXSS_COMPILER || (process.platform === 'darwin'
  ? '/Applications/wechatwebdevtools.app/Contents/Resources/app.asar.unpacked/node_modules/wcc-exec/wcsc' : '')
if (process.env.WECHAT_WXSS_COMPILER && !existsSync(compiler)) throw new Error(`找不到 WXSS 编译器：${compiler}`)
if (compiler && existsSync(compiler)) {
  for (const file of styles) {
    const result = spawnSync(compiler, [path.relative(root, file)], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'ignore', 'pipe'] })
    if (result.error || result.status !== 0) throw new Error(`WXSS 编译失败：${path.relative(root, file)}\n${result.error?.message || result.stderr}`)
  }
  console.log(`WeChat WXSS compilation passed: ${styles.length} files.`)
} else {
  console.log('WXSS native compiler unavailable; verify compilation in WeChat DevTools or set WECHAT_WXSS_COMPILER.')
}
console.log(`WeChat package valid: ${(size / 1024).toFixed(0)} KiB / 2048 KiB.`)
